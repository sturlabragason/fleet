package status

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
)

// StateDir is where a tool's own hooks drop what it is doing: one
// <agent>.json per agent under ~/.fleet/status (DESIGN §4, ADR-49). It is
// the event source that replaces polling for tools that have hooks.
func StateDir(home string) string { return filepath.Join(home, "status") }

// State is one hook report: the status the tool's own event implies, when it
// happened and which event said so.
type State struct {
	Status     Status    `json:"status"`
	WaitingFor string    `json:"waiting_for,omitempty"`
	Event      string    `json:"event,omitempty"`
	At         time.Time `json:"at"`
}

// hookEvents maps a tool hook name to the status it implies. Claude Code's
// names are the canonical set, Copilot's (agentStop, userPromptSubmitted)
// are aliases; names are lower-cased and stripped of - and _ before lookup
// so a profile may spell them either way. The subagent events change no
// status (a background subagent ending says nothing about its parent);
// they keep the subagent list instead (RecordSubs, ADR-96).
var hookEvents = map[string]Status{
	"stop":                Idle, // the turn finished: the agent is back at its prompt
	"agentstop":           Idle,
	"notification":        Waiting, // permission prompt or idle nudge: it needs me
	"pretooluse":          Working,
	"posttooluse":         Working,
	"posttoolusefailure":  Working,
	"userpromptsubmit":    Working,
	"userpromptsubmitted": Working,
	"sessionstart":        Working,
	"precompact":          Working,
	"sessionend":          Idle,
	// permissionrequest is left out: Copilot fires it before --allow-all
	// approves the call (seen live, ADR-96), so it would show "waiting"
	// for the length of every command. The screen, the ACP protocol and
	// Claude's Notification catch real prompts.
}

// HookEvents are the event names `fleet hook` knows, for the CLI's usage text.
func HookEvents() []string {
	return []string{"agentstop", "notification", "posttooluse", "posttoolusefailure", "precompact", "pretooluse",
		"sessionend", "sessionstart", "stop", "subagentstart", "subagentstop", "userpromptsubmit", "userpromptsubmitted"}
}

// SubagentEvent reports whether event starts or stops a subagent.
func SubagentEvent(event string) bool {
	e := normEvent(event)
	return e == "subagentstart" || e == "subagentstop"
}

// EventState turns a hook event name into the state it implies.
// ok=false for an event fleet does not know, so an unknown hook never
// overrides what the screen says.
func EventState(event, waitingFor string, now time.Time) (State, bool) {
	st, ok := hookEvents[normEvent(event)]
	if !ok {
		return State{}, false
	}
	return State{Status: st, WaitingFor: waitingFor, Event: normEvent(event), At: now}, true
}

func normEvent(e string) string {
	out := make([]rune, 0, len(e))
	for _, r := range e {
		if r == '-' || r == '_' || r == ' ' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		out = append(out, r)
	}
	return string(out)
}

func statePath(dir, agent string) string { return filepath.Join(dir, agent+".json") }

// WriteState stores one hook report for agent under dir (atomic rename, so a
// reader never sees half a file). Writing also bumps dir's mtime, which is
// what wakes the watcher.
func WriteState(dir, agent string, s State) error {
	if agent == "" {
		return fmt.Errorf("no agent name")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := statePath(dir, agent) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(dir, agent))
}

// ReadState reads the last hook report for agent. ok=false when there is
// none, or the file is unreadable or broken (the screen then decides).
func ReadState(dir, agent string) (State, bool) {
	b, err := os.ReadFile(statePath(dir, agent))
	if err != nil {
		return State{}, false
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		logx.For("status").Warn("broken hook state", "agent", agent, "err", err)
		return State{}, false
	}
	if s.Status == "" || s.At.IsZero() {
		return State{}, false
	}
	return s, true
}

// DropState removes an agent's hook state (on close, so a new agent of the
// same name does not inherit it).
func DropState(dir, agent string) {
	if agent == "" {
		return
	}
	_ = os.Remove(statePath(dir, agent))
}
