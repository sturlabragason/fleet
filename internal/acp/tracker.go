package acp

import (
	"strings"
	"sync"
)

// Statuses a Tracker reports; they match fleet's board statuses.
const (
	StatusStarting = "starting"
	StatusWorking  = "working"
	StatusWaiting  = "waiting"
	StatusIdle     = "idle"
	StatusError    = "error"
)

// State is an ACP agent's status. WaitingFor is the open question while
// waiting; Event names what caused the status ("acp:permission",
// "copilot:assistant.idle").
type State struct {
	Status     string
	WaitingFor string
	Event      string
}

// Tracker is the status state machine of one ACP agent. It does no I/O and
// is safe for concurrent use. Rules (DESIGN §7d):
//   - starting until the session is established (Ready) or the first prompt;
//   - a prompt sent, or a foreground assistant.turn_start, means working;
//   - the last outstanding prompt's response means idle, or error if it failed;
//   - assistant.idle or session.idle from the foreground agent means idle;
//   - a tool call or agent message while idle (background work) means working;
//   - an open permission question means waiting and dominates all of the
//     above until every question is answered;
//   - the agent exiting means error, for good.
//
// Copilot events with an agentId belong to a subagent and change nothing.
type Tracker struct {
	mu       sync.Mutex
	base     State // status without open questions
	open     []question
	inflight int
	exited   bool
}

type question struct {
	id    string
	text  string
	event string
}

// NewTracker returns a tracker in the starting state.
func NewTracker() *Tracker {
	return &Tracker{base: State{Status: StatusStarting, Event: "acp:start"}}
}

// Ready records the session established (session/new, load or resume
// answered): starting becomes idle. Other states are kept.
func (t *Tracker) Ready() State {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.base.Status == StatusStarting {
		t.set(StatusIdle, "acp:ready")
	}
	return t.stateLocked()
}

// PromptSent records a session/prompt sent.
func (t *Tracker) PromptSent() State {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inflight++
	t.set(StatusWorking, "acp:prompt")
	return t.stateLocked()
}

// PromptDone records a session/prompt response (or failure). Only the last
// prompt in flight ends the turn: a steer that fails while the turn runs
// changes nothing.
func (t *Tracker) PromptDone(r StopReason, err error) State {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inflight > 0 {
		t.inflight--
	}
	switch {
	case t.inflight > 0:
		// A steering prompt answered (or rejected) while the turn runs on.
	case err != nil:
		t.open = nil
		t.set(StatusError, "acp:prompt-error")
	default:
		// The turn is over, so any question the agent left open is moot.
		t.open = nil
		ev := "acp:" + string(r)
		if r == "" {
			ev = "acp:prompt-done"
		}
		t.set(StatusIdle, ev)
	}
	return t.stateLocked()
}

// Update records a session/update.
func (t *Tracker) Update(u SessionUpdate) State {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.base.Status == StatusIdle {
		switch u.Kind {
		case UpdateToolCall, UpdateToolCallUpdate, UpdateAgentMessage:
			t.set(StatusWorking, "acp:"+u.Kind)
		}
	}
	return t.stateLocked()
}

// PermissionAsked records an open session/request_permission.
func (t *Tracker) PermissionAsked(p RequestPermissionParams) State {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.exited {
		t.open = append(t.open, question{id: p.ToolCall.ToolCallID, text: questionText(p), event: "acp:permission"})
	}
	return t.stateLocked()
}

// PermissionAnswered closes the question about toolCallID.
func (t *Tracker) PermissionAnswered(toolCallID string) State {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, q := range t.open {
		if q.id == toolCallID {
			t.open = append(t.open[:i:i], t.open[i+1:]...)
			if len(t.open) == 0 {
				t.base.Event = "acp:answered"
			}
			break
		}
	}
	return t.stateLocked()
}

// Copilot records a github.com/copilot/sessionEvent.
func (t *Tracker) Copilot(e CopilotEvent) State {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e.AgentID != "" {
		return t.stateLocked()
	}
	switch e.Type {
	case "assistant.turn_start":
		t.set(StatusWorking, "copilot:"+e.Type)
	case "assistant.idle", "session.idle":
		t.set(StatusIdle, "copilot:"+e.Type)
	}
	return t.stateLocked()
}

// Exited records the agent process ending; the tracker stays in error.
func (t *Tracker) Exited(err error) State {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.open = nil
	t.inflight = 0
	t.set(StatusError, "acp:exited")
	t.exited = true
	return t.stateLocked()
}

// State returns the current state.
func (t *Tracker) State() State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stateLocked()
}

func (t *Tracker) set(status, event string) {
	if t.exited {
		return
	}
	t.base = State{Status: status, Event: event}
}

func (t *Tracker) stateLocked() State {
	if len(t.open) > 0 {
		q := t.open[0]
		return State{Status: StatusWaiting, WaitingFor: q.text, Event: q.event}
	}
	return t.base
}

// questionText is "<title>: <opt1> / <opt2>".
func questionText(p RequestPermissionParams) string {
	title := p.ToolCall.Title
	if title == "" {
		title = p.ToolCall.Kind
	}
	if title == "" {
		title = "permission"
	}
	names := make([]string, 0, len(p.Options))
	for _, o := range p.Options {
		n := o.Name
		if n == "" {
			n = o.OptionID
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		return title
	}
	return title + ": " + strings.Join(names, " / ")
}
