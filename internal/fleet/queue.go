package fleet

import (
	"sort"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/status"
)

// StartTimeout is how long a claimed queued agent may go without a pane
// before it is marked error, and how long an "unknown" agent (just started,
// not classified yet) counts as busy.
var StartTimeout = 2 * time.Minute

// busy reports whether an agent counts against max_working at now. unknown
// counts only for StartTimeout, so agents whose screen never matches a
// pattern can't freeze the queue; stuck stays busy (real CPU or a hang).
func busy(a *board.Agent, now time.Time) bool {
	switch status.Status(a.Status) {
	case status.Working, status.Stuck, status.Starting:
		return true
	case status.Unknown:
		since := a.StatusSince
		if since.IsZero() {
			since = a.Created
		}
		return now.Sub(since) < StartTimeout
	}
	return false
}

// pending reports whether an agent is queued or being started (no pane yet).
func pending(st string) bool {
	return st == string(status.Queued) || st == string(status.Starting)
}

func countBusy(b *board.Board, now time.Time) int {
	n := 0
	for _, a := range b.Agents {
		if busy(a, now) {
			n++
		}
	}
	return n
}

// hasPending reports whether any agent is queued or starting.
func hasPending(b *board.Board) bool {
	for _, a := range b.Agents {
		if pending(a.Status) {
			return true
		}
	}
	return false
}

// Busy is how many agents on the board count against max_working.
func (f *Fleet) Busy() int {
	return countBusy(f.Store.Load(), time.Now())
}

// QueueOrder returns the queued agents in start order: oldest Created first,
// then name. The drain starts [0]; the TUI shows its index+1 as "#n".
func QueueOrder(as []*board.Agent) []*board.Agent {
	var q []*board.Agent
	for _, a := range as {
		if a.Status == string(status.Queued) {
			q = append(q, a)
		}
	}
	sort.SliceStable(q, func(i, j int) bool {
		if !q[i].Created.Equal(q[j].Created) {
			return q[i].Created.Before(q[j].Created)
		}
		return q[i].Name < q[j].Name
	})
	return q
}

// enqueue writes the agent as queued when max_working is reached or others
// are already waiting (FIFO: a new spawn never jumps the queue). It returns
// nil, nil when there is a free slot and the caller should launch. wantWT is
// kept on the entry so the drain makes the same worktree choice the spawn
// asked for, whatever config.toml says by then.
func (f *Fleet) enqueue(name, profile, dir, prompt string, wantWT bool) (*board.Agent, error) {
	limit := f.Prefs.MaxWorking()
	if limit <= 0 {
		return nil, nil
	}
	var a *board.Agent
	n := 0
	err := f.Store.Update(func(b *board.Board) error {
		now := time.Now()
		if n = countBusy(b, now); n < limit && !hasPending(b) {
			return nil
		}
		a = &board.Agent{Name: name, Profile: profile, Patterns: f.Profiles[profile].Patterns, Dir: dir, Goal: prompt,
			Status: string(status.Queued), Created: now, StatusSince: now, NoWorktree: !wantWT}
		b.Agents[name] = a
		f.Store.AppendEvent(board.Event{Time: now, Agent: name, From: "", To: a.Status})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if a != nil {
		logx.For("fleet").Info("queued", "agent", name, "profile", profile, "dir", dir, "busy", n, "max_working", limit)
	}
	return a, nil
}

// claimQueued runs inside Refresh's board update: with a free slot it marks
// the next queued agent starting and returns a copy to launch.
func (f *Fleet) claimQueued(b *board.Board, now time.Time) *board.Agent {
	if limit := f.Prefs.MaxWorking(); limit > 0 && countBusy(b, now) >= limit {
		return nil
	}
	q := QueueOrder(b.Sorted())
	if len(q) == 0 {
		return nil
	}
	a := q[0]
	a.Status, a.StatusSince = string(status.Starting), now
	f.Store.AppendEvent(board.Event{Time: now, Agent: a.Name, From: string(status.Queued), To: a.Status})
	logx.For("fleet").Info("queue start", "agent", a.Name, "profile", a.Profile)
	c := *a
	return &c
}

// startQueued launches a claimed agent; Refresh runs it in a goroutine so the
// poll never waits on tmux or the tool's ready check.
func (f *Fleet) startQueued(a *board.Agent) {
	if _, err := f.launch(a.Name, a.Profile, a.Dir, a.Goal, true, !a.NoWorktree); err != nil {
		logx.For("fleet").Error("queue start failed", "agent", a.Name, "err", err)
		f.Store.Update(func(b *board.Board) error {
			// Only our claim turns into an error; a closed or re-spawned
			// agent is left alone.
			if cur, ok := b.Agents[a.Name]; ok && cur.Status == string(status.Starting) {
				now := time.Now()
				f.Store.AppendEvent(board.Event{Time: now, Agent: a.Name, From: cur.Status, To: string(status.Error)})
				cur.Status, cur.StatusSince, cur.LastLine = string(status.Error), now, "queue start failed: "+err.Error()
			}
			return nil
		})
	}
}

// keepPending is the status of a queued or starting agent that has no pane:
// unchanged, except a start that never got a pane turns into an error.
func keepPending(a *board.Agent, now time.Time) string {
	if a.Status == string(status.Starting) && now.Sub(a.StatusSince) > StartTimeout {
		a.LastLine = "queue start timed out"
		return string(status.Error)
	}
	return a.Status
}
