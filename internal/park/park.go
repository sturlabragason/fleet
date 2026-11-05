// Package park decides which agents may be parked and in what order, and
// reads how much memory the machine has left (DESIGN.md §7, ADR-44).
// Parking stops an idle agent's process and keeps its board entry and
// session id, so `fleet wake` resumes the same conversation in place.
package park

import (
	"sort"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/status"
)

// Parkable reports whether a status may be parked at all. Only an idle
// agent qualifies: a working, waiting, stuck, starting, queued, unknown or
// already parked agent is left alone, so parking never interrupts work and
// never drops an answer the agent is waiting for.
func Parkable(st string) bool { return status.Status(st) == status.Idle }

// Blocker says why an agent cannot be parked, "" when it can. pinned is the
// set of pinned panes (@fleet_pin): the user asked to keep those in sight.
func Blocker(a *board.Agent, pinned map[string]bool) string {
	switch {
	case a == nil:
		return "not on the board"
	case !Parkable(a.Status):
		return "it is " + a.Status + ", not idle"
	case a.SessionID == "":
		return "it has no session id, so waking it would lose the conversation"
	case pinned[a.Name]:
		return "it is pinned"
	}
	return ""
}

// Candidates returns the agents the memory guard may park, longest idle
// first (ties by name), which is the order it parks them in.
func Candidates(as []*board.Agent, pinned map[string]bool) []*board.Agent {
	var out []*board.Agent
	for _, a := range as {
		if Blocker(a, pinned) == "" {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].StatusSince.Equal(out[j].StatusSince) {
			return out[i].StatusSince.Before(out[j].StatusSince)
		}
		return out[i].Name < out[j].Name
	})
	return out
}
