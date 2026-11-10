package fake

// The fake's ACP server mode (`fakeagent --acp`, DESIGN §7d, ADR-74): a
// deterministic agent speaking newline-delimited JSON-RPC 2.0 so fleet's ACP
// driver can be tested without a real tool. The prompt text picks the
// scenario:
//
//	(default)  agent_message_chunk "fake: <prompt>", one completed tool_call, end_turn
//	[ask]      request_permission allow_once "Allow" / reject_once "Reject" before the
//	           tool call; reject → message "rejected"; cancelled outcome → cancelled
//	[slow N]   N ms between updates (200 without N)
//	[hang]     runs until session/cancel (stopReason cancelled) or a steering prompt
//	[crash]    exits 3 mid-turn
//	[error]    a JSON-RPC error on the prompt
//	[subagent] a turn_start/idle Copilot event pair with agentId "sub1"
//	[invoke]   an agy invoke_subagent tool call (Role "helper"): in_progress,
//	           then completed after the [slow] gap
//	[rich]     a thought, a tool call pending then completed after the [slow]
//	           gap, and a markdown reply, for looking at the pane (ADR-85)
//
// Every prompt's response carries usage, as agy's does: 100 input and 10
// output tokens.
//
// Copilot session events are sent only to a client that opted in through
// initialize's _meta, as copilot does. A prompt arriving while a turn runs
// steers it (agy): its work runs inside the running turn, which then ends
// end_turn, answering every prompt of the turn in arrival order.

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sturlabragason/fleet/internal/acp"
)

// ACPOptions configures ServeACP.
type ACPOptions struct {
	Name     string         // agentInfo title
	Resume   bool           // offer session/resume
	NoLoad   bool           // do not offer session/load
	StateDir string         // where sessions persist as <id>.json
	Exit     func(code int) // how [crash] exits; os.Exit when nil
	NewID    func() string  // session ids; random when nil
}

// ErrCrashed is returned by ServeACP when a [crash] turn called Exit and Exit
// returned (only in tests).
var ErrCrashed = errors.New("fake: crashed")

// DefaultStateDir is where sessions persist without --state.
func DefaultStateDir() string {
	if d := os.Getenv("FAKE_ACP_STATE"); d != "" {
		return d
	}
	return filepath.Join(os.TempDir(), "fleet-fake-acp")
}

type histEntry struct {
	Role string `json:"role"` // user or agent
	Text string `json:"text"`
}

type sessionFile struct {
	ID      string      `json:"id"`
	Cwd     string      `json:"cwd"`
	History []histEntry `json:"history"`
	// What the last host gave it (ADR-76): the mode it set, the MCP servers
	// and the env named in FAKE_ACP_ENV, for tests to check.
	Mode string            `json:"mode,omitempty"`
	MCP  []acp.MCPServer   `json:"mcp,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
}

// recordEnv is the env named in FAKE_ACP_ENV (comma-separated) that is set.
func recordEnv() map[string]string {
	out := map[string]string{}
	for _, k := range strings.Split(os.Getenv("FAKE_ACP_ENV"), ",") {
		if v, ok := os.LookupEnv(k); ok && k != "" {
			out[k] = v
		}
	}
	return out
}

type session struct {
	sessionFile
	turn *turn
}

type turn struct {
	ids    []json.RawMessage // prompt request ids, arrival order
	queue  []string          // texts still to work through
	steer  chan struct{}     // a steering prompt was queued
	cancel chan struct{}     // closed by session/cancel
}

type server struct {
	opt    ACPOptions
	out    io.Writer
	wmu    sync.Mutex
	mu     sync.Mutex
	events map[string]bool // Copilot events the client opted in to
	sess   map[string]*session
	pend   map[string]chan acp.Message // our requests awaiting a response
	nextID int
	nCall  int
	done   chan struct{}
	once   sync.Once
	wg     sync.WaitGroup
}

// ServeACP serves one ACP client on in/out until in ends (nil) or a [crash]
// turn exits.
func ServeACP(in io.Reader, out io.Writer, opt ACPOptions) error {
	if opt.Exit == nil {
		opt.Exit = os.Exit
	}
	if opt.NewID == nil {
		opt.NewID = randomID
	}
	if opt.StateDir == "" {
		opt.StateDir = DefaultStateDir()
	}
	s := &server{opt: opt, out: out, sess: map[string]*session{},
		pend: map[string]chan acp.Message{}, done: make(chan struct{})}
	lines := make(chan []byte)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for sc.Scan() {
			b := append([]byte(nil), sc.Bytes()...)
			select {
			case lines <- b:
			case <-s.done:
				return
			}
		}
	}()
	for {
		select {
		case <-s.done:
			return ErrCrashed
		case b, ok := <-lines:
			if !ok {
				s.shutdown()
				return nil
			}
			s.handle(b)
		}
	}
}

func randomID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "fake-" + hex.EncodeToString(b[:])
}

// shutdown cancels running turns and waits for them at EOF.
func (s *server) shutdown() {
	s.mu.Lock()
	for _, ss := range s.sess {
		if ss.turn != nil {
			closeOnce(ss.turn.cancel)
		}
	}
	for k, ch := range s.pend {
		close(ch)
		delete(s.pend, k)
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func closeOnce(c chan struct{}) {
	select {
	case <-c:
	default:
		close(c)
	}
}

func (s *server) write(m acp.Message) {
	m.JSONRPC = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, _ = s.out.Write(append(b, '\n'))
}

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func (s *server) reply(id json.RawMessage, result any) {
	s.write(acp.Message{ID: id, Result: raw(result)})
}

func (s *server) fail(id json.RawMessage, code int, msg string) {
	s.write(acp.Message{ID: id, Error: &acp.RPCError{Code: code, Message: msg}})
}

func (s *server) notify(method string, params any) {
	s.write(acp.Message{Method: method, Params: raw(params)})
}

func (s *server) handle(b []byte) {
	var m acp.Message
	if err := json.Unmarshal(b, &m); err != nil {
		return
	}
	if m.Method == "" { // a response to our request
		s.mu.Lock()
		ch := s.pend[string(m.ID)]
		delete(s.pend, string(m.ID))
		s.mu.Unlock()
		if ch != nil {
			ch <- m
		}
		return
	}
	isReq := len(m.ID) > 0
	switch m.Method {
	case acp.MethodInitialize:
		s.initialize(m)
	case acp.MethodSessionNew:
		var p acp.NewSessionParams
		_ = json.Unmarshal(m.Params, &p)
		id := s.opt.NewID()
		f := sessionFile{ID: id, Cwd: p.Cwd, History: []histEntry{}, MCP: p.MCPServers, Env: recordEnv()}
		s.mu.Lock()
		s.sess[id] = &session{sessionFile: f}
		s.mu.Unlock()
		s.save(f)
		s.reply(m.ID, acp.NewSessionResult{SessionID: id})
	case acp.MethodSessionLoad:
		if s.opt.NoLoad {
			s.fail(m.ID, acp.CodeMethodNotFound, "method not found: "+m.Method)
			return
		}
		s.open(m, true)
	case acp.MethodSessionResume:
		if !s.opt.Resume {
			s.fail(m.ID, acp.CodeMethodNotFound, "method not found: "+m.Method)
			return
		}
		s.open(m, false)
	case acp.MethodSessionPrompt:
		s.prompt(m)
	case acp.MethodSessionSetMode:
		var p acp.SetModeParams
		_ = json.Unmarshal(m.Params, &p)
		s.mu.Lock()
		ss := s.sess[p.SessionID]
		var f sessionFile
		if ss != nil {
			ss.Mode = p.ModeID
			f = ss.sessionFile
			f.History = append([]histEntry(nil), ss.History...)
		}
		s.mu.Unlock()
		if ss == nil || p.ModeID == "" {
			s.fail(m.ID, acp.CodeInvalidParams, "unknown session or mode")
			return
		}
		s.save(f)
		s.reply(m.ID, struct{}{})
		s.update(p.SessionID, acp.SessionUpdate{Kind: acp.UpdateModeChange, ModeID: p.ModeID})
	case acp.MethodSessionCancel:
		var p acp.SessionIDParams
		_ = json.Unmarshal(m.Params, &p)
		s.mu.Lock()
		if ss := s.sess[p.SessionID]; ss != nil && ss.turn != nil {
			closeOnce(ss.turn.cancel)
		}
		s.mu.Unlock()
	case acp.MethodSessionClose:
		var p acp.SessionIDParams
		_ = json.Unmarshal(m.Params, &p)
		s.mu.Lock()
		ss := s.sess[p.SessionID]
		if ss != nil {
			if ss.turn != nil {
				closeOnce(ss.turn.cancel)
			}
			delete(s.sess, p.SessionID)
		}
		s.mu.Unlock()
		if ss == nil {
			s.fail(m.ID, acp.CodeInvalidParams, "unknown session "+p.SessionID)
			return
		}
		s.reply(m.ID, struct{}{})
	default:
		if isReq {
			s.fail(m.ID, acp.CodeMethodNotFound, "method not found: "+m.Method)
		}
	}
}

func (s *server) initialize(m acp.Message) {
	var p struct {
		ClientCapabilities struct {
			Meta map[string]struct {
				Events []string `json:"events"`
			} `json:"_meta"`
		} `json:"clientCapabilities"`
	}
	_ = json.Unmarshal(m.Params, &p)
	ev := map[string]bool{}
	for _, e := range p.ClientCapabilities.Meta["github.com/copilot"].Events {
		ev[e] = true
	}
	s.mu.Lock()
	s.events = ev
	s.mu.Unlock()
	caps := acp.AgentCapabilities{LoadSession: !s.opt.NoLoad,
		SessionCapabilities: acp.SessionCapabilities{Close: json.RawMessage(`{}`)}}
	if s.opt.Resume {
		caps.SessionCapabilities.Resume = json.RawMessage(`{}`)
	}
	s.reply(m.ID, acp.InitializeResult{ProtocolVersion: acp.ProtocolVersion,
		AgentCapabilities: caps, AgentInfo: &acp.Implementation{Name: "fakeagent", Title: s.opt.Name, Version: "1"}})
}

// open serves session/load (replay true) and session/resume.
func (s *server) open(m acp.Message, replay bool) {
	var p acp.SessionRefParams
	_ = json.Unmarshal(m.Params, &p)
	f, err := s.load(p.SessionID)
	if err != nil {
		s.fail(m.ID, acp.CodeInvalidParams, "unknown session "+p.SessionID)
		return
	}
	if p.Cwd != "" {
		f.Cwd = p.Cwd
	}
	f.MCP, f.Env = p.MCPServers, recordEnv()
	s.save(f)
	s.mu.Lock()
	s.sess[f.ID] = &session{sessionFile: f}
	s.mu.Unlock()
	if replay {
		for _, h := range f.History {
			kind := acp.UpdateAgentMessage
			if h.Role == "user" {
				kind = acp.UpdateUserMessage
			}
			s.update(f.ID, acp.SessionUpdate{Kind: kind, Content: raw(acp.TextBlock(h.Text))})
		}
	}
	s.reply(m.ID, struct{}{})
}

func (s *server) path(id string) string {
	return filepath.Join(s.opt.StateDir, filepath.Base(id)+".json")
}

func (s *server) load(id string) (sessionFile, error) {
	var f sessionFile
	if id == "" || strings.ContainsAny(id, `/\`) {
		return f, errors.New("bad id")
	}
	b, err := os.ReadFile(s.path(id))
	if err != nil {
		return f, err
	}
	err = json.Unmarshal(b, &f)
	return f, err
}

func (s *server) save(f sessionFile) {
	if err := os.MkdirAll(s.opt.StateDir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(s.path(f.ID), raw(f), 0o600)
}

func (s *server) update(id string, u acp.SessionUpdate) {
	s.notify(acp.MethodSessionUpdate, acp.SessionNotification{SessionID: id, Update: u})
}

func (s *server) copilot(id, typ, agent string, data any) {
	s.mu.Lock()
	on := s.events[typ]
	s.mu.Unlock()
	if !on {
		return
	}
	s.notify(acp.MethodCopilotEvent, acp.CopilotEvent{SessionID: id, Type: typ, AgentID: agent, Data: raw(data)})
}

func promptText(p acp.PromptParams) string {
	var parts []string
	for _, b := range p.Prompt {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (s *server) prompt(m acp.Message) {
	var p acp.PromptParams
	_ = json.Unmarshal(m.Params, &p)
	text := promptText(p)
	s.mu.Lock()
	ss := s.sess[p.SessionID]
	if ss == nil {
		s.mu.Unlock()
		s.fail(m.ID, acp.CodeInvalidParams, "unknown session "+p.SessionID)
		return
	}
	if strings.Contains(text, "[error]") {
		s.mu.Unlock()
		s.fail(m.ID, acp.CodeInternal, "fake: error")
		return
	}
	ss.History = append(ss.History, histEntry{"user", text})
	if t := ss.turn; t != nil { // steer the running turn
		t.ids = append(t.ids, m.ID)
		t.queue = append(t.queue, text)
		select {
		case t.steer <- struct{}{}:
		default:
		}
		s.mu.Unlock()
		return
	}
	t := &turn{ids: []json.RawMessage{m.ID}, queue: []string{text},
		steer: make(chan struct{}, 1), cancel: make(chan struct{})}
	ss.turn = t
	s.wg.Add(1)
	s.mu.Unlock()
	go s.run(ss, t)
}

var slowRe = regexp.MustCompile(`\[slow(?:\s+(\d+))?\]`)

// run works through a turn's texts, steering prompts included, then answers
// every prompt of the turn.
func (s *server) run(ss *session, t *turn) {
	defer s.wg.Done()
	id := ss.ID
	s.copilot(id, "assistant.turn_start", "", struct{}{})
	stop := acp.StopEndTurn
	for {
		s.mu.Lock()
		if len(t.queue) == 0 {
			ss.turn = nil
			s.mu.Unlock()
			break
		}
		text := t.queue[0]
		t.queue = t.queue[1:]
		s.mu.Unlock()
		if r := s.work(ss, t, text); r != "" {
			stop = r
			if r == "crash" {
				return
			}
			s.mu.Lock()
			if ss.turn == t {
				ss.turn = nil
			}
			s.mu.Unlock()
			break
		}
	}
	s.mu.Lock()
	ids := t.ids
	f := ss.sessionFile
	f.History = append([]histEntry(nil), ss.History...)
	s.mu.Unlock()
	s.save(f)
	s.copilot(id, "assistant.idle", "", map[string]bool{"aborted": stop == acp.StopCancelled})
	s.copilot(id, "session.idle", "", struct{}{})
	for _, rid := range ids {
		s.reply(rid, acp.PromptResult{StopReason: stop, Usage: &acp.Usage{InputTokens: 100, OutputTokens: 10, TotalTokens: 110}})
	}
}

// work does one text's updates. It returns "" to go on, cancelled, or
// "crash".
func (s *server) work(ss *session, t *turn, text string) acp.StopReason {
	id := ss.ID
	var gap time.Duration
	if m := slowRe.FindStringSubmatch(text); m != nil {
		gap = 200 * time.Millisecond
		if n, err := strconv.Atoi(m[1]); err == nil {
			gap = time.Duration(n) * time.Millisecond
		}
	}
	pause := func() bool {
		if gap == 0 {
			select {
			case <-t.cancel:
				return false
			default:
				return true
			}
		}
		select {
		case <-t.cancel:
			return false
		case <-time.After(gap):
			return true
		}
	}
	say := func(msg string) {
		s.mu.Lock()
		ss.History = append(ss.History, histEntry{"agent", msg})
		s.mu.Unlock()
		s.update(id, acp.SessionUpdate{Kind: acp.UpdateAgentMessage, Content: raw(acp.TextBlock(msg))})
	}
	if strings.Contains(text, "[subagent]") {
		s.copilot(id, "assistant.turn_start", "sub1", struct{}{})
		s.copilot(id, "assistant.idle", "sub1", map[string]bool{"aborted": false})
	}
	say("fake: " + text)
	if strings.Contains(text, "[crash]") {
		s.opt.Exit(3)
		s.once.Do(func() { close(s.done) })
		return "crash"
	}
	if strings.Contains(text, "[hang]") {
		select {
		case <-t.cancel:
			return acp.StopCancelled
		case <-t.steer:
		}
	}
	if !pause() {
		return acp.StopCancelled
	}
	s.mu.Lock()
	s.nCall++
	call := fmt.Sprintf("call_%d", s.nCall)
	s.mu.Unlock()
	if strings.Contains(text, "[ask]") {
		res, ok := s.ask(id, call, t)
		if !ok || res.Outcome.Outcome == "cancelled" {
			return acp.StopCancelled
		}
		if res.Outcome.OptionID != "allow" {
			say("rejected")
			return ""
		}
		if !pause() {
			return acp.StopCancelled
		}
	}
	if strings.Contains(text, "[invoke]") {
		in := json.RawMessage(`{"Prompt":"help","Role":"helper","TypeName":"research"}`)
		s.update(id, acp.SessionUpdate{Kind: acp.UpdateToolCall, ToolCallID: call, Name: acp.SubagentTool,
			RawInput: in, Title: "Run invoke_subagent", ToolKind: "other", Status: "in_progress"})
		if !pause() {
			return acp.StopCancelled
		}
		s.update(id, acp.SessionUpdate{Kind: acp.UpdateToolCallUpdate, ToolCallID: call, Name: acp.SubagentTool,
			RawInput: in, Status: "completed"})
		return ""
	}
	if strings.Contains(text, "[rich]") {
		s.update(id, acp.SessionUpdate{Kind: acp.UpdateAgentThought, Content: raw(acp.TextBlock(
			"I'm weighing whether to build the board as one HTML file or as a page per section, " +
				"since the reviewers open it offline and one file travels better."))})
		s.update(id, acp.SessionUpdate{Kind: acp.UpdateToolCall, ToolCallID: call,
			Title: "Build reference board HTML", ToolKind: "execute", Status: "pending"})
		if !pause() {
			return acp.StopCancelled
		}
		s.update(id, acp.SessionUpdate{Kind: acp.UpdateToolCallUpdate, ToolCallID: call, Status: "completed"})
		say("## Reference board\n**DO-NOT list:** no gradients, no `box-shadow`, one accent.\n" +
			"- one self-contained file, see [the style guide](docs/style.md)\n" +
			"- images inlined with `scripts/inline-html.py`, so it renders offline in the sandboxed browser\n" +
			"1. build\n2. check it renders")
		return ""
	}
	s.update(id, acp.SessionUpdate{Kind: acp.UpdateToolCall, ToolCallID: call,
		Title: "fake tool", ToolKind: "execute", Status: "completed"})
	return ""
}

// ask sends request_permission and waits for the answer; ok is false when
// the turn was cancelled first or the input ended. A JSON-RPC error answer
// counts as a rejection.
func (s *server) ask(id, call string, t *turn) (acp.RequestPermissionResult, bool) {
	ch := make(chan acp.Message, 1)
	s.mu.Lock()
	s.nextID++
	rid := json.RawMessage(strconv.Itoa(s.nextID))
	s.pend[string(rid)] = ch
	s.mu.Unlock()
	s.write(acp.Message{ID: rid, Method: acp.MethodRequestPermission, Params: raw(acp.RequestPermissionParams{
		SessionID: id,
		ToolCall:  acp.ToolCallRef{ToolCallID: call, Title: "fake tool", Kind: "execute"},
		Options: []acp.PermissionOption{
			{OptionID: "allow", Name: "Allow", Kind: "allow_once"},
			{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
		},
	})})
	select {
	case <-t.cancel:
		s.mu.Lock()
		delete(s.pend, string(rid))
		s.mu.Unlock()
		return acp.RequestPermissionResult{}, false
	case m, open := <-ch:
		if !open {
			return acp.RequestPermissionResult{}, false
		}
		var r acp.RequestPermissionResult
		if m.Error != nil || json.Unmarshal(m.Result, &r) != nil {
			return acp.Selected("reject"), true
		}
		return r, true
	}
}
