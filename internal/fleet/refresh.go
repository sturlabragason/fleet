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
	"github.com/sturlabragason/fleet/internal/team"
	"github.com/sturlabragason/fleet/internal/theme"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// Refresh captures every agent pane, updates statuses on the board, logs
// transitions and returns the agents sorted by name.
func (f *Fleet) Refresh() ([]*board.Agent, error) {
	var snap *tmux.Snapshot
	var panes []tmux.PaneInfo
	var perr error
	if f.ShareSnapshot != nil {
		if snap, perr = f.T.Snapshot(); perr == nil {
			panes = snap.Panes
		}
	} else {
		panes, perr = f.T.ListPanes("")
	}
	if perr != nil {
		logx.For("status").Debug("list panes", "err", perr)
	}
	byAgent := map[string]bool{}
	dead := map[string]bool{}
	onWall := map[string]bool{}
	ids := map[string]string{}
	var agentPanes []tmux.PaneInfo
	tagged := map[string]string{}
	taggedNR := map[string]string{}
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
			taggedNR[p.Agent] = p.NoReport
		}
	}
	now := time.Now()
	var out []*board.Agent
	var claimed *board.Agent
	var stuck []stuckNote
	var silent []stuckNote
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
	var cs []ClientSeen
	if perr == nil {
		cs = f.clients()
	}
	err := f.Store.Update(func(b *board.Board) error {
		for _, a := range b.Agents {
			noteUser(a, cs, ids[a.Name])
			prev := a.Status
			var next string
			if !a.ACP() && f.skipCapture(a, byAgent[a.Name] && !dead[a.Name], onWall[a.Name], now) {
				next = a.Status
			} else {
				next = f.classify(a, byAgent[a.Name], dead[a.Name], ids[a.Name], now)
			}
			if perr == nil {
				// A tmux hiccup must not end every running subagent.
				f.subagents(a, byAgent[a.Name] && !dead[a.Name], now)
			}
			if next != prev {
				if newTurn(a, prev, next, now) {
					a.TurnAt = now
				}
				a.Status, a.StatusSince = next, now
				logx.For("status").Info("transition", "agent", a.Name, "from", prev, "to", next, "line", a.LastLine)
				f.Store.AppendEvent(board.Event{Time: now, Agent: a.Name, From: prev, To: next})
				f.tagPane(ids[a.Name], a.Shown(), now)
			} else if id := ids[a.Name]; id != "" && tagged[a.Name] != a.Shown() {
				// The pane title lags the board (pane re-created, tag lost,
				// set by an older fleet, or a question asked or answered
				// while idle, ADR-90): re-sync without resetting the age.
				f.tagPane(id, a.Shown(), a.StatusSince)
			}
			nr := a.NoReportTag(now)
			if id := ids[a.Name]; id != "" && nr != taggedNR[a.Name] {
				f.tagNoReport(id, nr)
			}
			// Only the hard flag wakes the owner: the user's agents get the
			// soft mark and nobody types into a pane the user is in.
			if tell && nr == "1" && a.NoReportTold.Before(a.PromptAt) {
				a.NoReportTold = now
				silent = append(silent, stuckNote{name: a.Name, profile: a.Profile, pane: ids[a.Name], line: a.LastLine})
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
	for _, n := range silent {
		go f.reportNoReport(n)
	}
	f.syncTeams(agentPanes)
	if snap != nil {
		f.shareSnapshot(snap, out, now)
	}
	if f.DrainQueue && perr == nil {
		f.guardMemory(out, now, low, free, mem)
	}
	if f.Housekeeping && perr == nil {
		f.DeliverWake()
		f.sweepDone(out, now)
		f.sweepChecks(out)
		f.reapDead(out, deadPanes, now)
		f.watchAdmirals(panes, now)
		f.checkRecycle(out, now)
	}
	return out, err
}

// shareSnapshot brings the pane tags this refresh just wrote (status,
// no-report mark, team) into snap, so readers see what tmux now holds, and
// hands it to ShareSnapshot.
func (f *Fleet) shareSnapshot(snap *tmux.Snapshot, agents []*board.Agent, now time.Time) {
	by := make(map[string]*board.Agent, len(agents))
	for _, a := range agents {
		by[a.Name] = a
	}
	var teams map[string]team.Info
	if f.Teams != nil {
		teams = f.Teams.ByAgent()
	}
	for i := range snap.Panes {
		p := &snap.Panes[i]
		a, ok := by[p.Agent]
		if !ok {
			continue
		}
		if st := a.Shown(); p.Status != st {
			p.Status, p.StatusSince = st, a.StatusSince.UnixNano()
		}
		p.NoReport = a.NoReportTag(now)
		if teams != nil {
			t := teams[p.Agent]
			p.Team, p.TeamColor = t.Team, t.Color
		}
	}
	f.ShareSnapshot(snap)
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
	if a.ACP() {
		return f.classifyACP(a, dead, pane, now)
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

// newTurn reports whether a switch from prev to next starts a new turn: the
// first work, work after starting, or work after an idle of at least
// NoReportAfter. A shorter idle, a menu or a stuck spell is inside the turn
// (ADR-68). An idle of any length starts one when it began after a question
// or blocked report and fleet or the user typed since: that is the answer
// (ADR-90).
func newTurn(a *board.Agent, prev, next string, now time.Time) bool {
	if next != string(status.Working) {
		return false
	}
	return a.TurnAt.IsZero() || prev == string(status.Starting) ||
		prev == string(status.Idle) && (now.Sub(a.StatusSince) >= board.NoReportAfter || answeredNow(a))
}

// answeredNow reports whether the idle that is ending began after the
// agent's question or blocked report and someone typed to it since.
func answeredNow(a *board.Agent) bool {
	r := a.Report
	if r == nil || r.State != "question" && r.State != "blocked" || a.StatusSince.Before(r.At) {
		return false
	}
	return a.PromptAt.After(r.At) || a.UserAt.After(r.At)
}

// tagNoReport sets or clears @fleet_noreport, the border's "…" mark for an
// agent that went idle mid-task without a report: "1", or "user" for the
// user's agent, drawn muted (ADR-68).
func (f *Fleet) tagNoReport(pane, tag string) {
	if tag != "" {
		f.T.SetPaneOption(pane, "@fleet_noreport", tag)
		return
	}
	f.T.UnsetPaneOption(pane, "@fleet_noreport")
}

var paneGlyphs = map[string]string{"working": "○", "waiting": "●", "error": "✖", "stuck": "⧗", "idle": "○"}

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
