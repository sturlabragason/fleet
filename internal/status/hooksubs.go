package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
)

// SubsDir is where the tools' own SubagentStart/SubagentStop hooks keep each
// agent's subagents: one <agent>.json (ADR-99). It is apart from the status
// file because an ACP agent's host owns that one and rewrites it whole.
func SubsDir(home string) string { return filepath.Join(StateDir(home), "subs") }

// Payload is what fleet reads from a hook's JSON on stdin. Claude Code sends
// snake_case fields, Copilot camelCase, and a Claude-format plugin under
// Copilot snake_case again, so both spellings are read.
type Payload struct {
	Message          string
	NotificationType string
	ToolName         string
	// AgentID is set when the hook fired inside a subagent (Copilot's
	// agentId, Claude's agent_id), or names the subagent of a
	// SubagentStart/SubagentStop.
	AgentID string
	// AgentType names the subagent (Claude's agent_type, Copilot's
	// agentType or agentName).
	AgentType string
}

// ParsePayload reads a hook's JSON; anything it can't read is empty.
func ParsePayload(b []byte) Payload {
	var r struct {
		Message   string `json:"message"`
		NotifS    string `json:"notification_type"`
		NotifC    string `json:"notificationType"`
		ToolS     string `json:"tool_name"`
		ToolC     string `json:"toolName"`
		AgentIDS  string `json:"agent_id"`
		AgentIDC  string `json:"agentId"`
		AgentTyS  string `json:"agent_type"`
		AgentTyC  string `json:"agentType"`
		AgentName string `json:"agentName"`
	}
	if json.Unmarshal(b, &r) != nil {
		return Payload{}
	}
	or := func(a, b string) string {
		if a != "" {
			return a
		}
		return b
	}
	return Payload{
		Message:          strings.TrimSpace(r.Message),
		NotificationType: or(r.NotifS, r.NotifC),
		ToolName:         or(r.ToolS, r.ToolC),
		AgentID:          or(r.AgentIDS, r.AgentIDC),
		AgentType:        or(r.AgentTyS, or(r.AgentTyC, r.AgentName)),
	}
}

// WaitingFor is the line a waiting event shows: the hook's message, else
// the notification's kind.
func (p Payload) WaitingFor(event string) string {
	switch {
	case p.Message != "":
		return p.Message
	case normEvent(event) == "notification":
		return strings.ReplaceAll(p.NotificationType, "_", " ")
	}
	return ""
}

// hookSubs is the subagents file: the list, and a counter that names a
// subagent whose start carried no id.
type hookSubs struct {
	List []Subagent `json:"list"`
	Seq  int        `json:"seq"`
}

// applySub folds one hook event into h. A start adds a running subagent; a
// stop ends the running one with its id, else the oldest running one of
// that type, else the oldest running one (Copilot's subagentStart carries
// no id); a session end fails whatever still runs.
func applySub(h hookSubs, event string, p Payload, now time.Time) hookSubs {
	h.List = clone(h.List)
	switch normEvent(event) {
	case "subagentstart":
		for _, s := range h.List {
			if p.AgentID != "" && s.ID == p.AgentID && s.State == SubRunning {
				return h
			}
		}
		h.Seq++
		id := p.AgentID
		if id == "" {
			id = "hook-" + strconv.Itoa(h.Seq)
		}
		name := p.AgentType
		if name == "" {
			name = "subagent"
		}
		h.List = append(h.List, Subagent{ID: id, AgentID: p.AgentID, Name: name, State: SubRunning, Started: now})
	case "subagentstop":
		i := -1
		for _, match := range []func(Subagent) bool{
			func(s Subagent) bool { return p.AgentID != "" && s.ID == p.AgentID },
			func(s Subagent) bool { return p.AgentType != "" && s.Name == p.AgentType },
			func(Subagent) bool { return true },
		} {
			for j, s := range h.List {
				if s.State == SubRunning && match(s) {
					i = j
					break
				}
			}
			if i >= 0 {
				break
			}
		}
		if i >= 0 {
			h.List[i].State, h.List[i].Ended = SubDone, now
		}
	case "sessionend":
		h.List = endRunning(h.List, now)
	default:
		return h
	}
	h.List = PruneSubagents(h.List, now)
	return h
}

func subsPath(home, agent string) string { return filepath.Join(SubsDir(home), agent+".json") }

// RecordSubs folds one hook event into agent's subagents file. Parallel
// subagents end at the same moment, so the read and write hold a lock.
// Events other than subagent start/stop and session end change nothing.
func RecordSubs(home, agent, event string, p Payload, now time.Time) error {
	switch e := normEvent(event); {
	case e == "sessionend":
		if _, err := os.Stat(subsPath(home, agent)); err != nil {
			return nil // no subagent ever started: nothing to end
		}
	case !SubagentEvent(e):
		return nil
	}
	dir := SubsDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lf, err := os.OpenFile(filepath.Join(dir, agent+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	h, _ := readSubs(home, agent)
	data, err := json.Marshal(applySub(h, event, p, now))
	if err != nil {
		return err
	}
	tmp := subsPath(home, agent) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, subsPath(home, agent))
}

func readSubs(home, agent string) (hookSubs, bool) {
	b, err := os.ReadFile(subsPath(home, agent))
	if err != nil {
		return hookSubs{}, false
	}
	var h hookSubs
	if err := json.Unmarshal(b, &h); err != nil {
		logx.For("status").Warn("broken subagents file", "agent", agent, "err", err)
		return hookSubs{}, false
	}
	return h, true
}

// ReadHookSubs returns the subagents agent's hooks recorded, pruned at now.
// ok=false when its hooks never reported a subagent: they are not live for
// it, and another source decides.
func ReadHookSubs(home, agent string, now time.Time) ([]Subagent, bool) {
	h, ok := readSubs(home, agent)
	if !ok {
		return nil, false
	}
	return PruneSubagents(clone(h.List), now), true
}

// DropHookSubs removes agent's subagents file, so a new agent of the same
// name starts with none.
func DropHookSubs(home, agent string) {
	if agent == "" {
		return
	}
	for _, p := range []string{subsPath(home, agent), filepath.Join(SubsDir(home), agent+".lock")} {
		_ = os.Remove(p)
	}
}
