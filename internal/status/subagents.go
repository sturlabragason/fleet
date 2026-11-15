package status

import (
	"bytes"
	"encoding/json"
	"sort"
	"time"
)

// Subagent states.
const (
	SubRunning = "running"
	SubDone    = "done"
	SubFailed  = "failed"
)

// Subagent is one subagent an agent launched, read from the tool's session
// event log (DESIGN.md §4 Subagents, ADR-18).
type Subagent struct {
	ID      string    `json:"id"` // toolCallId of the launching task call
	AgentID string    `json:"agent_id,omitempty"`
	Name    string    `json:"name"`
	State   string    `json:"state"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended"`
	// A write_agent call woke this subagent and has not reported back yet.
	// If it fails (agent not found, not in background mode) the wake is undone.
	WakeCall  string    `json:"wake_call,omitempty"`
	PrevState string    `json:"prev_state,omitempty"` // "" = restored from Known
	PrevEnded time.Time `json:"prev_ended"`
	// The subagent sent a message without tool requests in its current turn;
	// it ends at that turn's assistant.turn_end unless it calls a tool first.
	EndPending bool `json:"end_pending,omitempty"`
}

// KnownSub remembers which launch an agentId belongs to, so a write_agent to
// a subagent that was already pruned from the list can bring it back.
type KnownSub struct {
	AgentID string `json:"agent_id"`
	ID      string `json:"id"`
	Name    string `json:"name"`
}

// Subs is the scanner state: the visible list and the bounded agentId memory.
type Subs struct {
	List  []Subagent
	Known []KnownSub
}

// Keep finished subagents this long after they end, and at most SubMax in
// total; remember at most SubKnownMax agentIds (most recent).
const (
	SubKeep     = 5 * time.Minute
	SubMax      = 12
	SubKnownMax = 32
)

// Substrings that make a line worth decoding; everything else is skipped
// without touching the JSON.
var subMarks = [][]byte{
	[]byte(`"subagent.`), []byte(`"write_agent"`),
	[]byte(`"session.start"`), []byte(`"session.resume"`), []byte(`"session.shutdown"`),
}

var (
	execComplete = []byte(`"tool.execution_complete"`)
	assistantMsg = []byte(`"assistant.message"`)
	turnEnd      = []byte(`"assistant.turn_end"`)
	turnStart    = []byte(`"assistant.turn_start"`)
)

type subEvent struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	AgentID   string    `json:"agentId"`
	Data      struct {
		ToolCallID string          `json:"toolCallId"`
		AgentName  string          `json:"agentName"`
		Cancelled  bool            `json:"cancelled"`
		Success    *bool           `json:"success"`
		ToolName   string          `json:"toolName"`
		Arguments  json.RawMessage `json:"arguments"`
		// ToolRequests is empty on a subagent's final answer.
		ToolRequests []json.RawMessage `json:"toolRequests"`
	} `json:"data"`
}

// relevant reports whether a log line can change the list.
func relevant(line []byte, list []Subagent, known []KnownSub) bool {
	for _, m := range subMarks {
		if bytes.Contains(line, m) {
			return true
		}
	}
	if bytes.Contains(line, turnStart) {
		// A new turn of a finished subagent: it was re-woken, e.g. by a
		// message queued while it was busy.
		for _, s := range list {
			if s.State != SubRunning && s.AgentID != "" && bytes.Contains(line, []byte(`"agentId":"`+s.AgentID+`"`)) {
				return true
			}
		}
		for _, k := range known {
			if bytes.Contains(line, []byte(`"agentId":"`+k.AgentID+`"`)) {
				return true
			}
		}
		return false
	}
	exec := bytes.Contains(line, execComplete)
	msg := bytes.Contains(line, assistantMsg) || bytes.Contains(line, turnEnd)
	if !exec && !msg {
		return false
	}
	for _, s := range list {
		if s.State != SubRunning {
			continue
		}
		if exec && s.WakeCall != "" && bytes.Contains(line, []byte(s.WakeCall)) {
			return true
		}
		if exec && bytes.Contains(line, []byte(s.ID)) {
			return true
		}
		if msg && s.AgentID != "" && bytes.Contains(line, []byte(`"agentId":"`+s.AgentID+`"`)) {
			return true
		}
	}
	return false
}

// applyLine applies one complete log line. Broken or unrelated lines are ignored.
func applyLine(st Subs, line []byte, now time.Time) Subs {
	list := st.List
	if !relevant(line, list, st.Known) {
		return st
	}
	var e subEvent
	if err := json.Unmarshal(line, &e); err != nil {
		return st
	}
	ts := e.Timestamp
	if ts.IsZero() {
		ts = now
	}
	find := func(id string) int {
		for i := range list {
			if id != "" && list[i].ID == id {
				return i
			}
		}
		return -1
	}
	switch e.Type {
	case "subagent.started":
		if e.Data.ToolCallID == "" {
			return st
		}
		s := Subagent{ID: e.Data.ToolCallID, AgentID: e.AgentID, Name: e.Data.AgentName, State: SubRunning, Started: ts}
		if i := find(s.ID); i >= 0 {
			list[i] = s
		} else {
			list = append(list, s)
		}
		st.Known = remember(st.Known, KnownSub{AgentID: s.AgentID, ID: s.ID, Name: s.Name})
	case "subagent.completed":
		// A late completion (e.g. cancelled:true at shutdown for an idle
		// multi-turn agent) must not change a subagent that already finished.
		if i := find(e.Data.ToolCallID); i >= 0 && list[i].State == SubRunning {
			list[i].State, list[i].Ended = SubDone, ts
			if e.Data.Cancelled {
				list[i].State = SubFailed
			}
		}
	case "tool.execution_complete":
		list = settleWake(list, e.Data.ToolCallID, e.Data.Success == nil || *e.Data.Success)
		if i := find(e.Data.ToolCallID); i >= 0 && list[i].State == SubRunning && e.Data.Success != nil && !*e.Data.Success {
			list[i].State, list[i].Ended = SubFailed, ts
		}
	case "assistant.message":
		// A turn whose messages end without tool requests is the subagent's
		// final answer. After a write_agent re-wake this is often the only end
		// signal. A tool-less message can also come mid-turn, so it only arms
		// the end; a later message with tool requests disarms it.
		for i := range list {
			if e.AgentID != "" && list[i].AgentID == e.AgentID && list[i].State == SubRunning {
				list[i].EndPending = len(e.Data.ToolRequests) == 0
			}
		}
	case "assistant.turn_start":
		list = wake(list, st.Known, e.AgentID, "", ts)
	case "assistant.turn_end":
		for i := range list {
			if e.AgentID != "" && list[i].AgentID == e.AgentID && list[i].State == SubRunning && list[i].EndPending {
				list[i].State, list[i].Ended, list[i].EndPending = SubDone, ts, false
			}
		}
	case "tool.execution_start":
		if e.Data.ToolName == "write_agent" {
			for _, id := range writeTargets(e.Data.Arguments) {
				list = wake(list, st.Known, id, e.Data.ToolCallID, ts)
			}
		}
	case "session.start", "session.resume", "session.shutdown":
		list = endRunning(list, ts)
	}
	st.List = list
	return st
}

// wake marks the subagent with agentId id running again on behalf of the
// write_agent call call (empty: a new turn seen in the log, nothing to undo).
// One pruned from the list comes back from known, started at t; an unknown id
// changes nothing.
func wake(list []Subagent, known []KnownSub, id, call string, t time.Time) []Subagent {
	if id == "" {
		return list
	}
	for i := range list {
		if list[i].AgentID == id {
			if list[i].State != SubRunning {
				if call != "" {
					list[i].WakeCall, list[i].PrevState, list[i].PrevEnded = call, list[i].State, list[i].Ended
				}
				list[i].State, list[i].Ended, list[i].EndPending = SubRunning, time.Time{}, false
			}
			return list
		}
	}
	for _, k := range known {
		if k.AgentID == id {
			return append(list, Subagent{ID: k.ID, AgentID: id, Name: k.Name, State: SubRunning, Started: t, WakeCall: call})
		}
	}
	return list
}

// settleWake resolves wakes made by the write_agent call call: kept when it
// succeeded, undone when it failed.
func settleWake(list []Subagent, call string, ok bool) []Subagent {
	if call == "" {
		return list
	}
	out := list[:0]
	for _, s := range list {
		if s.WakeCall != call {
			out = append(out, s)
			continue
		}
		if !ok && s.State == SubRunning {
			if s.PrevState == "" {
				continue
			}
			s.State, s.Ended = s.PrevState, s.PrevEnded
		}
		s.WakeCall, s.PrevState, s.PrevEnded = "", "", time.Time{}
		out = append(out, s)
	}
	return out
}

// remember records k as the most recent known subagent, keeping SubKnownMax.
func remember(known []KnownSub, k KnownSub) []KnownSub {
	if k.AgentID == "" {
		return known
	}
	out := known[:0:0]
	for _, x := range known {
		if x.AgentID != k.AgentID {
			out = append(out, x)
		}
	}
	out = append(out, k)
	if len(out) > SubKnownMax {
		out = out[len(out)-SubKnownMax:]
	}
	return out
}

// writeTargets returns the agent ids a write_agent call addresses.
func writeTargets(raw json.RawMessage) []string {
	var one struct {
		AgentID string `json:"agent_id"`
	}
	var many struct {
		AgentIDs []string `json:"agent_ids"`
	}
	_ = json.Unmarshal(raw, &one)
	_ = json.Unmarshal(raw, &many)
	return append(many.AgentIDs, one.AgentID)
}

// endRunning marks every running subagent failed at t.
func endRunning(list []Subagent, t time.Time) []Subagent {
	for i := range list {
		if list[i].State == SubRunning {
			list[i].State, list[i].Ended = SubFailed, t
		}
	}
	return list
}

// EndSubagents marks running subagents failed (their process is gone) and prunes.
func EndSubagents(list []Subagent, now time.Time) []Subagent {
	return PruneSubagents(endRunning(clone(list), now), now)
}

// PruneSubagents keeps running subagents and those that ended within SubKeep,
// at most SubMax (oldest finished go first, then oldest running), in start order.
func PruneSubagents(list []Subagent, now time.Time) []Subagent {
	out := list[:0:0]
	for _, s := range list {
		if s.State == SubRunning || now.Sub(s.Ended) <= SubKeep {
			out = append(out, s)
		}
	}
	for len(out) > SubMax {
		drop := -1
		for i, s := range out {
			if s.State != SubRunning && (drop < 0 || s.Ended.Before(out[drop].Ended)) {
				drop = i
			}
		}
		if drop < 0 {
			drop = 0
			for i, s := range out {
				if s.Started.Before(out[drop].Started) {
					drop = i
				}
			}
		}
		out = append(out[:drop], out[drop+1:]...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	if len(out) == 0 {
		return nil
	}
	return out
}

// RunningCount returns how many subagents are running.
func RunningCount(list []Subagent) int {
	n := 0
	for _, s := range list {
		if s.State == SubRunning {
			n++
		}
	}
	return n
}

func clone(l []Subagent) []Subagent { return append([]Subagent(nil), l...) }

func (s Subs) clone() Subs {
	return Subs{List: clone(s.List), Known: append([]KnownSub(nil), s.Known...)}
}
