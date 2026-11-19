package fleet

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/cgroup"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/park"
	"github.com/sturlabragason/fleet/internal/playbook"
	"github.com/sturlabragason/fleet/internal/proc"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/tools"
)

// Park stops an idle agent's process and keeps it on the board as parked,
// with its session id, so Wake resumes the same conversation in the same
// directory (DESIGN §7, ADR-44). A working, waiting, stuck or starting
// agent, one sitting on a prompt, and a pinned agent are refused: parking
// must never interrupt work or drop an answer the agent is waiting for.
// reason is logged and shown on the tile ("" = the user asked).
//
// The claim comes before the kill: the board entry flips to parked under
// the board lock, re-checking there, so an agent that started working
// between the check and the kill is left alone instead of being killed
// mid-thought. A failed kill puts the entry back as it was.
func (f *Fleet) Park(name, reason string) error { return f.park(name, reason, false) }

// ParkRunaway parks an agent whatever its status, pin or prompt: the
// resource guard's stop for an agent whose processes are taking the machine
// down (ADR-122). Waking it resumes the session when it has one.
func (f *Fleet) ParkRunaway(name, reason string) error { return f.park(name, reason, true) }

// blocker is park.Blocker, or for a forced park only "already parked".
func blocker(a *board.Agent, pinned map[string]bool, force bool) string {
	switch {
	case !force:
		return park.Blocker(a, pinned)
	case a == nil:
		return "not on the board"
	case a.Status == string(status.Parked):
		return "it is already parked"
	}
	return ""
}

func (f *Fleet) park(name, reason string, force bool) error {
	a, ok := f.Store.Load().Agents[name]
	if !ok {
		return fmt.Errorf("no agent %q", name)
	}
	// One listing for both the pin set and the pane: a tmux error here
	// must refuse the park, not silently look like "no pane, not pinned".
	ps, err := f.T.ListPanes("")
	if err != nil {
		if f.T.Running() {
			return fmt.Errorf("can't park %s: reading tmux failed: %w", name, err)
		}
		ps = nil // no server: no panes to read and none to kill
	}
	pinned, pane := map[string]bool{}, ""
	for _, p := range ps {
		if p.Agent == "" {
			continue
		}
		if p.Pin {
			pinned[p.Agent] = true
		}
		if p.Agent == name {
			pane = p.ID
		}
	}
	if a.ACP() && a.SessionID == "" {
		// Parked before a refresh read it: the transcript has it.
		if sid := acphost.LastSession(acphost.TranscriptPath(f.Home, name)); sid != "" {
			a.SessionID = sid
			if err := f.Store.Update(func(b *board.Board) error {
				if cur, ok := b.Agents[name]; ok && cur.SessionID == "" {
					cur.SessionID = sid
				}
				return nil
			}); err != nil {
				logx.For("fleet").Warn("session id not saved", "agent", name, "err", err)
			}
		}
	}
	if why := blocker(a, pinned, force); why != "" {
		return fmt.Errorf("can't park %s: %s", name, why)
	}
	w, err := "", error(nil)
	if !force {
		w, err = f.Prompting(name)
	}
	switch {
	case err != nil && pane != "":
		// Fail closed: an unreadable screen may be a prompt.
		return fmt.Errorf("can't park %s: reading its screen failed: %w", name, err)
	case w != "":
		return fmt.Errorf("can't park %s: it is on a prompt (%s)", name, status.Cut(w, 60))
	}
	if !force && pane != "" && !a.ACP() {
		if d := f.draftIn(name, pane); d != "" {
			return fmt.Errorf("can't park %s: its input box holds unsubmitted text (%q)", name, status.Cut(d, 40))
		}
	}
	line := "parked · fleet wake " + name
	if reason != "" {
		line = "parked (" + reason + ") · fleet wake " + name
	}
	var prev string
	var prevSince time.Time
	var prevSubs []status.Subagent
	if err := f.Store.Update(func(b *board.Board) error {
		cur, ok := b.Agents[name]
		if !ok {
			return fmt.Errorf("no agent %q", name)
		}
		if why := blocker(cur, pinned, force); why != "" {
			return fmt.Errorf("can't park %s: %s", name, why)
		}
		now := time.Now()
		prev, prevSince, prevSubs = cur.Status, cur.StatusSince, cur.Subagents
		f.Store.AppendEvent(board.Event{Time: now, Agent: name, From: cur.Status, To: string(status.Parked)})
		cur.Status, cur.StatusSince, cur.PaneID, cur.LastLine = string(status.Parked), now, "", line
		cur.Subagents = nil
		return nil
	}); err != nil {
		return err
	}
	if pane != "" {
		if a.ACP() {
			f.acpStop(name)
		}
		if err := f.KillPane(pane); err != nil {
			if err := f.Store.Update(func(b *board.Board) error {
				if cur, ok := b.Agents[name]; ok && cur.Status == string(status.Parked) {
					cur.Status, cur.StatusSince, cur.PaneID, cur.LastLine = prev, prevSince, pane, ""
					cur.Subagents = prevSubs
				}
				return nil
			}); err != nil {
				logx.For("fleet").Warn("park failed and the board still says parked", "agent", name, "err", err)
			}
			return fmt.Errorf("park %s: %w", name, err)
		}
	}
	logx.For("fleet").Info("parked", "agent", name, "session_id", a.SessionID, "reason", reason)
	return nil
}

// treeGrace is how long a pane's leftover processes get between SIGTERM
// and SIGKILL.
var treeGrace = 2 * time.Second

// KillPane kills a pane and every process that ran under it. tmux only
// hangs up on the pane's own process, so children that ignore SIGHUP or
// left its process group (a Copilot voice server, a build) would outlive
// the agent (#26). The tree is read before the kill, while the parent
// links still lead to the pane, and only stopped if the kill succeeded.
func (f *Fleet) KillPane(pane string) error {
	var tree []proc.Proc
	// A dead pane's pid may already belong to another process.
	if out, err := f.T.Run("display-message", "-p", "-t", pane, "#{pane_dead} #{pane_pid} #{pid}"); err == nil {
		if fs := strings.Fields(out); len(fs) == 3 && fs[0] == "0" {
			if pid, err := strconv.Atoi(fs[1]); err == nil {
				tree = proc.Tree(pid)
				if server, err := strconv.Atoi(fs[2]); err == nil {
					reclaimCache(pane, pid, server)
				}
			}
		}
	}
	if err := f.T.KillPane(pane); err != nil {
		return err
	}
	proc.Kill(tree, treeGrace)
	return nil
}

// reclaimWait bounds how long a close or park waits for the kernel to drop
// the pane's page cache.
const reclaimWait = time.Second

// reclaimCache drops the page cache charged to the pane's own cgroup v2
// scope before the pane goes, so it does not linger in a dying cgroup and
// count against the parent slice's MemoryHigh (ADR-139). A pane in the
// tmux server's cgroup (tmux without per-pane scopes) is skipped: that
// cache is every pane's. Best effort: failures are logged at debug.
func reclaimCache(pane string, pid, server int) {
	log := logx.For("cgroup")
	dir, err := cgroup.Of(pid)
	if err != nil {
		log.Debug("reclaim: no scope", "pane", pane, "pid", pid, "err", err)
		return
	}
	if sd, err := cgroup.Of(server); err != nil || sd == dir {
		log.Debug("reclaim: pane shares the tmux server's cgroup", "pane", pane, "cgroup", dir)
		return
	}
	ctx, cancel := context.WithTimeout(context.TODO(), reclaimWait) // TODO: KillPane takes no ctx yet
	defer cancel()
	n, err := cgroup.ReclaimCache(ctx, dir)
	if err != nil {
		log.Debug("reclaim: failed", "pane", pane, "cgroup", dir, "bytes", n, "err", err)
		return
	}
	log.Debug("reclaim: dropped page cache", "pane", pane, "cgroup", dir, "bytes", n)
}

// Wake resumes a parked agent where it stopped: the profile's resume
// command on the same session id, in the same dir and worktree, under the
// same name (DESIGN §7, ADR-44). It is the adopt path without the session
// lookup, since fleet stopped that process itself. While memory is still
// under the guard's floor it refuses, so a wake isn't parked again on the
// next check; force skips that (the user can see the machine).
func (f *Fleet) Wake(name string, force bool) (*board.Agent, error) {
	old, ok := f.Store.Load().Agents[name]
	if !ok {
		return nil, fmt.Errorf("no agent %q", name)
	}
	if old.Status != string(status.Parked) {
		return nil, fmt.Errorf("agent %q is %s, not parked", name, old.Status)
	}
	p, ok := f.Profiles[old.Profile]
	if !ok {
		return nil, fmt.Errorf("unknown profile %q (have %v)", old.Profile, tools.Names(f.Profiles))
	}
	if len(p.Resume) == 0 && !old.ACP() {
		return nil, fmt.Errorf("profile %s can't resume", old.Profile)
	}
	if old.ACP() && old.SessionID == "" {
		return nil, fmt.Errorf("agent %q has no ACP session id recorded; it can't be resumed", name)
	}
	// A parked entry with a live pane is a park that did not take; waking
	// it would put a second process on the same session.
	if pane := f.paneOf(name); pane != "" {
		return nil, fmt.Errorf("agent %q still has pane %s; it is not parked", name, pane)
	}
	if low, free, m := f.LowMemory(); low && !force {
		return nil, fmt.Errorf("not waking %s: %s; park something else, or force it (fleet wake %s --force, fleet_park force=true)", name, LowMemoryNote(free, m), name)
	}
	// Claim it under the board lock, so two wakes (the CLI and the
	// admiral's MCP server) can never start two processes on one session.
	if err := f.Store.Update(func(b *board.Board) error {
		cur, ok := b.Agents[name]
		if !ok {
			return fmt.Errorf("no agent %q", name)
		}
		if cur.Status != string(status.Parked) {
			return fmt.Errorf("agent %q is %s, not parked", name, cur.Status)
		}
		now := time.Now()
		f.Store.AppendEvent(board.Event{Time: now, Agent: name, From: cur.Status, To: string(status.Starting)})
		cur.Status, cur.StatusSince = string(status.Starting), now
		return nil
	}); err != nil {
		return nil, err
	}
	// A woken agent comes back in the mode it was spawned in: the tool is
	// started fresh, so its permission mode has to be asked for again
	// (ADR-56), and so does its context level (ADR-60).
	wpr := f.Prefs.For(old.Profile)
	// The level is the one spawn recorded; an adopted agent has none and
	// gets no context args, as at adopt.
	role := Role{Leader: old.Leader, LeadBy: old.LeadBy, Interactive: old.Interactive, Admiral: old.Admiral}
	ctx := ""
	if old.Effort != "" {
		ctx = contextKey(p, playbook.Level(old.Effort), role.Leader)
	}
	// The model it was started with (ADR-112); an agent from before ADR-100
	// gets the profile's pin.
	p, model, err := f.withModel(old.Profile, p, old.Model)
	if err != nil {
		f.unclaimWake(name, old)
		return nil, err
	}
	var argv []string
	if old.ACP() {
		var err error
		w := *old
		w.Model = model
		if argv, p, err = f.wakeACPArgv(&w, p, ctx); err != nil {
			f.unclaimWake(name, old)
			return nil, err
		}
		// The parked host's last state (idle) must not stand for the new
		// host before it is up; the transcript and session id stay.
		f.forgetHook(name)
	} else {
		argv, _ = p.Argv(f.launchVars(name, old.SessionID, p, wpr, fenceDir(old.Worktree, wpr.FenceWrites), false),
			tools.Launch{Resume: true, Mode: f.modeFor(name, old.Profile, p, wpr.Mode), Extra: modelExtra(p, model, wpr.ExtraArgs), Context: ctx})
	}
	a, err := f.start(name, old.Profile, p, old.Dir, old.Goal, "", old.SessionID, argv, true,
		startExtra{Base: old.Baseline, Worktree: old.Worktree, NoWorktree: old.NoWorktree, Card: old.Card, Effort: old.Effort, Model: model, Role: role, Driver: old.Driver, Config: old.Config})
	if err != nil {
		f.unclaimWake(name, old)
		return nil, err
	}
	// start writes a fresh entry; keep the history the parked one carried.
	if err := f.Store.Update(func(b *board.Board) error {
		if cur, ok := b.Agents[name]; ok {
			cur.Created, cur.Report, cur.UserAt = old.Created, old.Report, old.UserAt
			cur.PromptAt, cur.NoReportTold, cur.TurnAt = old.PromptAt, old.NoReportTold, old.TurnAt
			cur.RecycleTold, cur.DoneBounces = old.RecycleTold, old.DoneBounces
			cur.SubLogOffset, cur.SubKnown = old.SubLogOffset, old.SubKnown
			a = cur
		}
		return nil
	}); err != nil {
		logx.For("fleet").Warn("woke, but its history was not carried over", "agent", name, "err", err)
	}
	logx.For("fleet").Info("woke", "agent", name, "session_id", old.SessionID, "pane", a.PaneID)
	return a, nil
}

// unclaimWake puts a failed wake back on the board as parked.
func (f *Fleet) unclaimWake(name string, old *board.Agent) {
	if err := f.Store.Update(func(b *board.Board) error {
		if cur, ok := b.Agents[name]; ok && cur.Status == string(status.Starting) {
			cur.Status, cur.StatusSince, cur.LastLine = string(status.Parked), time.Now(), old.LastLine
		}
		return nil
	}); err != nil {
		logx.For("fleet").Warn("wake failed and the board still says starting", "agent", name, "err", err)
	}
}

// pinned is the set of agents whose pane the user pinned (@fleet_pin): the
// memory guard and `fleet park` leave them alone.
func (f *Fleet) pinned() map[string]bool {
	ps, err := f.T.ListPanes("")
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, p := range ps {
		if p.Agent != "" && p.Pin {
			out[p.Agent] = true
		}
	}
	return out
}
