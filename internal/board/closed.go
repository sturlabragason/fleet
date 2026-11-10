package board

import (
	"sort"
	"time"

	"github.com/sturlabragason/fleet/internal/jsonx"
)

// Closed is what a close keeps of an agent so it can be resumed (ADR-95):
// its last board entry, the team it was in, and when it closed.
type Closed struct {
	Agent Agent     `json:"agent"`
	Team  string    `json:"team,omitempty"`
	At    time.Time `json:"at"`
	extra jsonx.Extra
}

// Closed records are kept for ClosedKeep, at most ClosedMax of them.
const (
	ClosedKeep = 24 * time.Hour
	ClosedMax  = 50
)

// AddClosed records a closed agent and prunes the old records. The copy
// loses its pane and live state: it is a record, not an agent.
func (b *Board) AddClosed(a *Agent, team string, now time.Time) {
	c := &Closed{Agent: *a, Team: team, At: now}
	c.Agent.PaneID, c.Agent.Exit, c.Agent.Closing, c.Agent.Stuck = "", nil, time.Time{}, nil
	if b.Closed == nil {
		b.Closed = map[string]*Closed{}
	}
	b.Closed[a.Name] = c
	b.PruneClosed(now)
}

// ClosedRecord is name's record if it is younger than ClosedKeep.
func (b *Board) ClosedRecord(name string, now time.Time) (*Closed, bool) {
	c, ok := b.Closed[name]
	if !ok || now.Sub(c.At) > ClosedKeep {
		return nil, false
	}
	return c, true
}

// PruneClosed drops records older than ClosedKeep, then the oldest past
// ClosedMax.
func (b *Board) PruneClosed(now time.Time) {
	var keep []*Closed
	for n, c := range b.Closed {
		if now.Sub(c.At) > ClosedKeep {
			delete(b.Closed, n)
			continue
		}
		keep = append(keep, c)
	}
	if len(keep) <= ClosedMax {
		return
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].At.After(keep[j].At) })
	for _, c := range keep[ClosedMax:] {
		delete(b.Closed, c.Agent.Name)
	}
}
