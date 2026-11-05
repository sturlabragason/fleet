package fleet

import (
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/theme"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// Refresh captures every agent pane, updates statuses on the board, logs
// transitions and returns the agents sorted by name.
func (f *Fleet) Refresh() ([]*board.Agent, error) {
	panes, perr := f.T.ListPanes("")
	if perr != nil {
		logx.For("status").Debug("list panes", "err", perr)
	}
	byAgent := map[string]bool{}
	dead := map[string]bool{}
	onWall := map[string]bool{}
	ids := map[string]string{}
	var agentPanes []tmux.PaneInfo
	tagged := map[string]string{}
	deadPanes := map[string]tmux.PaneInfo{}
	for _, p := range panes {
		if p.Agent != "" {
			byAgent[p.Agent] = true
			dead[p.Agent] = p.Dead
			if p.Dead {
				deadPanes[p.Agent] = p
			}
			ids[p.Agent] = p.ID
			onWall[p.Agent] = !strings.HasPrefix(p.Window, WindowPrefix)
			agentPanes = append(agentPanes, p)
			tagged[p.Agent] = p.Status
		}
	}
	now := time.Now()
	var out []*board.Agent
	var claimed *board.Agent
	var stuck []stuckNote
	// Only the overview claims and reports stuck episodes, so a one-shot
	// `fleet ls` or the admiral's MCP server -- which now drain the queue
	// too (ADR-44) -- neither wakes the admiral nor burns the one
	// notification this episode gets.
	tell := f.Housekeeping && perr == nil
	// One memory reading and one config read per poll, both taken before
	// the board lock: the drain and the guard agree on them, and no
	// process waits on the lock while another reads a file (§7, ADR-44).
	low, limit, free := false, 0, uint64(0)
	var mem prefs.Memory
	if f.DrainQueue {
		low, free, mem = f.LowMemory()
		limit = f.MaxWorking()
	}
	err := f.Store.Update(func(b *board.Board) error {
		for _, a := range b.Agents {
			prev := a.Status
			var next string
			if f.skipCapture(a, byAgent[a.Name] && !dead[a.Name], onWall[a.Name], now) {
				next = a.Status
			} else {
				next = f.classify(a, byAgent[a.Name], dead[a.Name], ids[a.Name], now)
			}
			if perr == nil {
				// A tmux hiccup must not end every running subagent.
				f.subagents(a, byAgent[a.Name] && !dead[a.Name], now)
			}
			if next != prev {
				a.Status, a.StatusSince = next, now
				logx.For("status").Info("transition", "agent", a.Name, "from", prev, "to", next, "line", a.LastLine)
				f.Store.AppendEvent(board.Event{Time: now, Agent: a.Name, From: prev, To: next})
				f.tagPane(ids[a.Name], next, now)
			} else if id := ids[a.Name]; id != "" && tagged[a.Name] != next {
				// The pane title lags the board (pane re-created, tag lost, or
				// set by an older fleet): re-sync without resetting the age.
				f.tagPane(id, next, a.StatusSince)
			}
			if tell && a.Stuck.Stuck() && !a.Stuck.Notified {
				a.Stuck.Notified = true
				stuck = append(stuck, stuckNote{name: a.Name, profile: a.Profile, pane: ids[a.Name], reason: a.Stuck.Reason, line: a.LastLine})
			}
		}
		// A failed pane listing makes every agent look gone (busy 0):
		// never drain on that.
		if f.DrainQueue && perr == nil {
			claimed = f.claimQueued(b, now, low, limit)
		}
		out = b.Sorted()
		return nil
	})
	if claimed != nil {
		if f.DrainSync {
			f.startQueued(claimed)
		} else {
			go f.startQueued(claimed)
		}
	}
	for _, n := range stuck {
		go f.reportStuck(n)
	}
	f.syncTeams(agentPanes)
	if f.DrainQueue && perr == nil {
		f.guardMemory(out, now, low, free, mem)
	}
	if f.Housekeeping && perr == nil {
		f.DeliverWake()
		f.sweepDone(out, now)
		f.sweepChecks(out)
		f.reapDead(out, deadPanes, now)
	}
	return out, err
}

// skipCapture reports whether this Refresh may reuse an agent's last status
// instead of capturing its pane: only for a live, known agent that is off the
// wall and was captured less than OffWallEvery ago.
func (f *Fleet) skipCapture(a *board.Agent, alive, wall bool, now time.Time) bool {
	if f.OffWallEvery <= 0 {
		return false
	}
	if f.lastCap == nil {
		f.lastCap = map[string]time.Time{}
	}
	st := status.Status(a.Status)
	if wall || !alive || pending(a.Status) || st == "" || st == status.Unknown || st == status.Gone || now.Sub(f.lastCap[a.Name]) >= f.OffWallEvery {
		f.lastCap[a.Name] = now
		return false
	}
	return true
}

// syncTeams keeps @fleet_team/@fleet_team_color on agent panes in line with
// teams.json, touching a pane only when its tags differ.
func (f *Fleet) syncTeams(panes []tmux.PaneInfo) {
	if f.Teams == nil {
		return
	}
	by := f.Teams.ByAgent()
	for _, p := range panes {
		want := by[p.Agent]
		if p.Team == want.Team && p.TeamColor == want.Color {
			continue
		}
		if want.Team == "" {
			f.T.UnsetPaneOption(p.ID, "@fleet_team")
			f.T.UnsetPaneOption(p.ID, "@fleet_team_color")
			continue
		}
		f.T.SetPaneOption(p.ID, "@fleet_team_color", want.Color)
		f.T.SetPaneOption(p.ID, "@fleet_team", want.Team)
	}
}

func (f *Fleet) classify(a *board.Agent, alive, dead bool, pane string, now time.Time) string {
	if !alive {
		a.Stuck = nil // nothing to grind on without a pane
		if pending(a.Status) {
			return keepPending(a, now)
		}
		// A parked agent has no process on purpose (DESIGN §7): it is not gone.
		if a.Status == string(status.Parked) {
			return a.Status
		}
		return string(status.Gone)
	}
	a.PaneID = pane
	text, err := f.T.Capture(pane, 0)
	if err != nil {
		return string(status.Unknown)
	}
	if dead {
		// A dead pane is reported by the reaper, not the stuck rule; leaving
		// the memory would re-assert a claimed episode when it comes back.
		a.Stuck = nil
		a.LastLine = "process exited"
		return string(status.Error)
	}
	h := fnv.New64a()
	h.Write([]byte(text))
	sum := h.Sum64()
	changed := sum != a.Hash
	if changed {
		a.Hash, a.LastChange = sum, now
	}
	if t := status.Tail(text, 1); len(t) > 0 {
		a.LastLine = t[0]
	}
	st, line, ok := f.Patterns[a.Patterns].Match(text)
	if ok && (st == status.Waiting || st == status.Error) && line != "" {
		// waiting_for: the prompt, not the footer under it.
		a.LastLine = line
	}
	// A menu on screen is a question for the user whatever the patterns
	// made of the rest of the pane: a picker nobody answers is waiting, not
	// working, and its own question is the thing to answer (DESIGN §4).
	if m, mok := f.Patterns[a.Patterns].Menu(text); mok {
		st, ok = status.Waiting, true
		if m.Question != "" {
			a.LastLine = m.Question
		}
	}
	switch {
	case !ok && changed:
		st = status.Working
	case !ok:
		st = status.Status(a.Status)
		// A live pane under a gone or parked entry is a park that did
		// not take: read it fresh rather than keep the stale label.
		if st == "" || st == status.Gone || st == status.Parked {
			st = status.Unknown
		}
	}
	errLine := line
	if st == status.Error && errLine == "" {
		errLine = a.LastLine
	}
	// A hook the tool fired itself beats a guess from the screen, and beats
	// the screen outright when it happened after the pane last changed
	// (DESIGN §18 #3, ADR-49).
	if hs, hok := f.hookState(a, ok); hok {
		st = applyHook(a, hs)
	}
	st, a.Stuck = status.NextStuck(a.Stuck, status.StuckIn{
		Status:  st,
		Line:    errLine,
		Changed: changed,
		Still:   now.Sub(a.LastChange),
		After:   f.Prefs.For(a.Profile).StuckAfter,
		Now:     now,
	})
	return string(st)
}

var paneGlyphs = map[string]string{"working": "⠹", "waiting": "●", "error": "✖", "stuck": "⧗", "idle": "○"}

// tagPane stores the status on the pane so tmux border formats can show it and
// the wall can evict and emphasise by status. since is the time the agent
// entered this status (now on a transition, the board's StatusSince on a re-sync).
func (f *Fleet) tagPane(pane, st string, since time.Time) {
	if pane == "" {
		return
	}
	c := theme.Status(st)
	g, ok := paneGlyphs[st]
	if !ok {
		g = "?"
	}
	f.T.SetPaneOption(pane, "@fleet_status", st)
	f.T.SetPaneOption(pane, "@fleet_status_since", strconv.FormatInt(since.UnixNano(), 10))
	f.T.SetPaneOption(pane, "@fleet_color", c)
	f.T.SetPaneOption(pane, "@fleet_glyph", g)
}
