package kagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/sturlabragason/fleet/internal/acp"
)

// turn is one session/prompt: a kagent task, streamed into session updates.
type turn struct {
	b      *Bridge
	sid    string
	said   bool   // agent text was shown this turn
	art    string // the artifact the last text belongs to
	reply  strings.Builder
	state  string // the task's last state
	final  *Task  // the task as last sent whole
	status string // the last status message's text
}

// prompt registers the turn before it returns, so a session/cancel read
// right after the prompt finds it, and runs the turn in the background.
func (b *Bridge) prompt(ctx context.Context, m acp.Message) {
	var p acp.PromptParams
	_ = json.Unmarshal(m.Params, &p)
	ctx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	s := b.sess[p.SessionID]
	var err error
	switch {
	case s == nil:
		err = &acp.RPCError{Code: acp.CodeInvalidParams, Message: "unknown session " + p.SessionID}
	case s.cancel != nil:
		err = &acp.RPCError{Code: acp.CodeInvalidParams, Message: "a turn is already running"}
	default:
		s.cancel = cancel
	}
	b.mu.Unlock()
	if err != nil {
		cancel()
		b.fail(m.ID, err)
		return
	}
	b.wg.Go(func() {
		defer cancel()
		res, err := b.runTurn(ctx, s, p)
		b.mu.Lock()
		s.cancel, s.taskID = nil, ""
		b.mu.Unlock()
		if err != nil {
			b.fail(m.ID, err)
			return
		}
		b.write(acp.Message{ID: m.ID, Result: raw(res)})
	})
}

func (b *Bridge) runTurn(ctx context.Context, s *session, p acp.PromptParams) (acp.PromptResult, error) {
	b.mu.Lock()
	st := s.state
	gw := b.Gateway
	b.mu.Unlock()
	text := promptText(p.Prompt)
	if st.Context == "" {
		text = b.preamble(ctx, st.Cwd) + text
	}
	t := &turn{b: b, sid: p.SessionID}
	err := gw.Send(ctx, st.Context, st.Task, text, func(ev Event) { t.on(s, ev) })
	// Whatever ended the turn, the session answers a task next time only
	// if that task now waits for input.
	t.settle(s)
	if ctx.Err() != nil {
		return acp.PromptResult{StopReason: acp.StopCancelled}, nil //nolint:nilerr // a cancelled turn is a stop reason, not an error
	}
	if err != nil {
		return acp.PromptResult{}, err
	}
	res, err := t.finish()
	if err == nil && res.StopReason == acp.StopEndTurn {
		b.fileReport(ctx, t.reply.String())
	}
	return res, err
}

// on handles one streamed event: the session learns its kagent ids before
// the agent's text is shown.
func (t *turn) on(s *session, ev Event) {
	var ctxID, taskID, text, art string
	var cont bool
	switch {
	case ev.Task != nil:
		ctxID, taskID, t.final = ev.Task.ContextID, ev.Task.ID, ev.Task
		t.setState(ev.Task.Status)
	case ev.StatusUpdate != nil:
		ctxID, taskID = ev.StatusUpdate.ContextID, ev.StatusUpdate.TaskID
		t.setState(ev.StatusUpdate.Status)
		if m := ev.StatusUpdate.Status.Message; m != nil {
			text = Text(m.Parts)
		}
	case ev.ArtifactUpdate != nil:
		u := ev.ArtifactUpdate
		ctxID, taskID, text = u.ContextID, u.TaskID, Text(u.Artifact.Parts)
		art, cont = u.Artifact.ArtifactID, u.Append
		t.tools(u.Artifact.Parts)
	case ev.Message != nil:
		ctxID, t.state = ev.Message.ContextID, "completed"
		text = Text(ev.Message.Parts)
	}
	b := t.b
	b.mu.Lock()
	if taskID != "" {
		s.taskID = taskID
	}
	changed := ctxID != "" && s.Context != ctxID
	if changed {
		s.Context = ctxID
	}
	st := s.state
	b.mu.Unlock()
	if changed {
		_ = b.save(st)
	}
	t.show(text, art, cont)
}

func (t *turn) setState(st Status) {
	if st.State != "" {
		t.state = State(st.State)
	}
	if st.Message != nil {
		t.status = Text(st.Message.Parts)
	}
}

// show draws text as a new paragraph, unless cont appends it to art, the
// artifact shown last.
func (t *turn) show(text, art string, cont bool) {
	if text == "" || (!t.said && strings.TrimSpace(text) == "") {
		return
	}
	if t.said && (!cont || art == "" || art != t.art) {
		text = "\n\n" + text
	}
	t.art = art
	t.said = true
	t.reply.WriteString(text)
	t.b.say(t.sid, text)
}

// tools draws kagent's function_call and function_response data parts as
// an ACP tool call and its completion.
func (t *turn) tools(parts []Part) {
	for _, p := range parts {
		var d struct {
			ID   string          `json:"id"`
			Name string          `json:"name"`
			Args json.RawMessage `json:"args"`
		}
		if p.Data == nil || json.Unmarshal(p.Data, &d) != nil || d.ID == "" {
			continue
		}
		// A missing or non-string part-type matches no case.
		switch p.Metadata["kagent.dev/a2a/part-type"] {
		case "function_call":
			t.b.update(t.sid, acp.SessionUpdate{Kind: acp.UpdateToolCall, ToolCallID: d.ID, Title: d.Name, Name: d.Name, RawInput: d.Args, Status: "in_progress"})
		case "function_response":
			t.b.update(t.sid, acp.SessionUpdate{Kind: acp.UpdateToolCallUpdate, ToolCallID: d.ID, Status: "completed"})
		}
		t.art = ""
	}
}

// finish turns the task's end state into the prompt's stop reason. A task
// that waits for input keeps its id, so the next prompt answers it.
func (t *turn) finish() (acp.PromptResult, error) {
	if !t.said && t.final != nil {
		for _, a := range t.final.Artifacts {
			t.show(Text(a.Parts), "", false)
		}
		t.show(t.status, "", false)
	}
	switch t.state {
	case "failed", "rejected":
		msg := t.status
		if msg == "" {
			msg = "the kagent task " + t.state
		}
		return acp.PromptResult{}, errors.New("kagent: " + msg)
	case "canceled":
		return acp.PromptResult{StopReason: acp.StopCancelled}, nil
	}
	return acp.PromptResult{StopReason: acp.StopEndTurn}, nil
}

// settle keeps the task's id when it waits for input, so the next prompt
// answers it, and drops it otherwise.
func (t *turn) settle(s *session) {
	b := t.b
	b.mu.Lock()
	waiting := ""
	if t.state == "input-required" || t.state == "auth-required" {
		waiting = s.taskID
	}
	changed := s.Task != waiting
	s.Task = waiting
	st := s.state
	b.mu.Unlock()
	if changed {
		_ = b.save(st)
	}
}

// reportLine is a reply's last line asking fleet to file a report.
var reportLine = regexp.MustCompile("^[\\s*`>]*fleet_report[\\s*`]+(done|blocked|question|progress)[*`]*:[\\s*`]*(.*?)[\\s*`]*$")

// fileReport files a reply's report: its last non-empty line, when that is
// a fleet_report line.
func (b *Bridge) fileReport(ctx context.Context, reply string) {
	reply = strings.TrimSpace(reply)
	m := reportLine.FindStringSubmatch(reply[strings.LastIndex(reply, "\n")+1:])
	if b.Report == nil || m == nil || m[2] == "" {
		return
	}
	if err := b.Report(ctx, m[1], m[2]); err != nil {
		slog.Warn("kagent report", "component", "kagent", "err", err)
	}
}

func promptText(blocks []acp.ContentBlock) string {
	var out []string
	for _, c := range blocks {
		switch {
		case c.Text != "":
			out = append(out, c.Text)
		case c.Type == "image":
			out = append(out, "[an image was attached; a cluster agent can't receive it]")
		}
	}
	return strings.Join(out, "\n")
}

// preamble starts a conversation: where the agent runs and where the code
// is, since the local worktree and fleet's MCP tools are not reachable.
func (b *Bridge) preamble(ctx context.Context, dir string) string {
	cluster := b.Cluster
	if cluster == "" {
		cluster = "the current kube context"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "[fleet] You run as a kagent Session in the Kubernetes cluster %s (agent %s), not on the user's machine. ", cluster, b.Gateway.Agent)
	sb.WriteString("Fleet's MCP tools and the user's local files are not reachable from here: answer in this conversation. When you finish, are blocked or need a decision, make the last line of your reply `fleet_report <done|blocked|question|progress>: <one-line headline>`; fleet files it as your report.")
	if b.Repo != nil && dir != "" {
		if url, branch := b.Repo(ctx, dir); url != "" {
			fmt.Fprintf(&sb, " The code is %s", url)
			if branch != "" {
				fmt.Fprintf(&sb, "; work on branch %s and push it", branch)
			}
			sb.WriteString(".")
		}
	}
	return sb.String() + "\n\n"
}
