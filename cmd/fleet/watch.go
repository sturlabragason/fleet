package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/netwatch"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/watch"
)

// cmdWatch runs the status watcher in the foreground: the one process that
// keeps the board, the pane border titles, `fleet ls` and the overview
// agreeing while nobody is looking (DESIGN §7c, ADR-49). `fleet up` starts
// it detached; this command is also how to run it by hand or in a test.
func cmdWatch(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	once := fs.Bool("once", false, "refresh once and exit")
	stop := fs.Bool("stop", false, "ask a running watcher to stop")
	interval := fs.Duration("interval", watch.DefaultInterval, "baseline refresh when no event fires")
	tick := fs.Duration("tick", watch.DefaultTick, "how often event sources are checked")
	probeOnly := fs.Bool("probe", false, "print ok and exit (a rebuilt binary is probed before the watcher re-execs into it)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *probeOnly:
		fmt.Println("ok")
		return nil
	case *stop:
		return stopWatcher(f)
	case *once:
		_, err := f.Refresh()
		return err
	}
	// Our own pid is the watcher re-exec'ing into a rebuilt binary (ADR-62):
	// its heartbeat is still fresh, and treating it as another watcher made
	// the new image exit at once, leaving fleet without one.
	if b, ok := watch.ReadBeat(f.Home); ok && b.PID != os.Getpid() && watch.Live(f.Home, time.Now()) {
		fmt.Printf("a watcher is already running (pid %d, since %s)\n", b.PID, b.Started.Format(time.Kitchen))
		return nil
	}
	// The watcher captures every agent on every pass: with no overview open
	// there is no wall, so "off the wall" would mean every agent and status
	// would lag by OffWallEvery (DESIGN §7a).
	f.OffWallEvery = 0
	f.DrainQueue = true
	// A resident watcher owns the once-a-poll housekeeping -- admiral
	// wakes above all, plus the done sweep, unclaimed done-checks and
	// dead-pane reaping (ADR-51). Without it a report that landed while
	// the admiral was busy stayed queued and was never retried, because
	// the overview skips its own refresh while a watcher is live (ADR-49).
	// `--once` leaves all of it alone: a one-shot must not auto-close an
	// agent, run somebody's done-check or claim a queued spawn it will
	// exit before starting.
	f.Housekeeping = true
	if netwatch.Enabled() { // outages are held, then resumed (ADR-110)
		f.NetProbe = netwatch.Probe
	}
	// Those done-checks may take minutes; off the loop they cannot stall it.
	f.Async()
	// Each refresh's one tmux read is shared, so the overview makes no
	// tmux calls of its own while this watcher is live (ADR-98).
	f.ShareSnapshot = func(s *tmux.Snapshot) {
		if err := watch.WriteSnapshot(f.Home, s); err != nil {
			logx.For("watch").Warn("snapshot", "err", err)
		}
	}
	w := &watch.Watcher{
		Home:     f.Home,
		Refresh:  func() error { _, err := f.Refresh(); return err },
		Paths:    f.WatchPaths,
		Stop:     func() bool { return !f.T.HasSession(fleet.Session) },
		Interval: *interval,
		Tick:     *tick,
		// A rebuilt bin/fleet takes over this process (ADR-62).
		Exe:    f.Exe,
		Reexec: func() error { return reexecWatcher(f.Exe) },
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	// A new release reaches the tmux server here, not only at `fleet up`:
	// this runs again each time the watcher re-execs into a new binary.
	hotApply(f)
	err := w.Run(ctx)
	if err == nil {
		watch.DropSnapshot(f.Home)
	}
	return err
}

// reexecWatcher replaces the watcher with the binary at exe, same pid and
// arguments, once `exe watch --probe` says it runs. A half-written build
// fails the probe and the old code keeps watching. The flock on watch.lock is
// close-on-exec, so the new image claims it again at start.
func reexecWatcher(exe string) error {
	if err := probeBinary(exe); err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}

// probeBinary checks that the binary at exe runs (`exe watch --probe` says
// ok) and was not replaced again meanwhile. A half-written build fails it.
func probeBinary(exe string) error {
	before, err := os.Stat(exe)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "watch", "--probe").Output()
	if err != nil {
		return fmt.Errorf("probe: %w", err)
	}
	if strings.TrimSpace(string(out)) != "ok" {
		return fmt.Errorf("probe said %q", strings.TrimSpace(string(out)))
	}
	if now, err := os.Stat(exe); err != nil || !os.SameFile(now, before) || !now.ModTime().Equal(before.ModTime()) {
		return fmt.Errorf("binary changed after the probe")
	}
	return nil
}

// stopWatcher signals a running watcher to exit (it drops its heartbeat on
// the way out, so the next `fleet up` starts a fresh one).
func stopWatcher(f *fleet.Fleet) error {
	// A pid is signalled only while the process still exists: a heartbeat
	// left by a crash names a pid that may since have been recycled. A
	// watcher whose refreshes are failing is still stoppable.
	pid, ok := watch.Running(f.Home)
	if !ok {
		watch.Clear(f.Home)
		fmt.Println("no watcher running")
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := p.Signal(syscall.SIGTERM); err != nil {
		watch.Clear(f.Home)
		fmt.Println("no watcher running")
		return nil
	}
	fmt.Printf("stopped the watcher (pid %d)\n", pid)
	return nil
}

// startWatcher launches `fleet watch` detached, unless one is already
// running from this binary. A watcher running another binary — deleted or
// replaced by a rebuild, from before watchers re-exec themselves — is stopped
// and replaced, so `fleet up` leaves exactly one watcher on the current code
// (ADR-62). It is best-effort: a fleet without a watcher still works, every
// command just refreshes for itself.
func startWatcher(f *fleet.Fleet) {
	log := logx.For("watch")
	// Running, not Live: a watcher whose refreshes are failing is still the
	// watcher, and a second one would only double the scraping.
	if pid, ok := watch.Running(f.Home); ok {
		if !watch.StaleExe(pid, f.Exe) {
			log.Debug("watcher already running", "pid", pid)
			return
		}
		log.Info("watcher runs an old binary; replacing it", "pid", pid, "exe", f.Exe)
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
			log.Error("stop old watcher", "pid", pid, "err", err)
			return
		}
		if !waitWatcherGone(f.Home, pid, 5*time.Second) {
			log.Error("old watcher did not stop; not starting another", "pid", pid)
			return
		}
	}
	cmd := exec.Command(f.Exe, "watch")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		log.Error("start watcher", "err", err)
		return
	}
	log.Info("started watcher", "pid", cmd.Process.Pid)
	_ = cmd.Process.Release()
}

// waitWatcherGone waits up to d for watcher pid to stop holding home.
func waitWatcherGone(home string, pid int, d time.Duration) bool {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		if p, ok := watch.Running(home); !ok || p != pid {
			return true
		}
	}
	return false
}

// hookStdinMax caps what a hook payload may contribute; tools send a small
// JSON object, and the point of reading it is not to block the tool.
const hookStdinMax = 64 << 10

// cmdHook records what an agent's own tool just did:
//
//	fleet hook <event> [--agent NAME] [--waiting-for TEXT] [--no-status]
//
// It writes ~/.fleet/status/<name>.json, which the watcher notices within a
// tick (DESIGN §18 #3, ADR-49), and keeps the agent's subagents from
// SubagentStart/SubagentStop (ADR-99). It is wired in through the per-agent
// plugin dir fleet writes at spawn (ADR-99), so the user's own settings are
// never touched. --no-status keeps the subagents only: an ACP agent's host
// owns its status file. A hook fired inside a subagent (its payload names
// one) changes no status. A Stop/agentStop of an agent that holds a task and
// has not reported is blocked once per prompt (ADR-105). It never fails loudly: a broken hook must not
// break the agent.
func cmdHook(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	agent := fs.String("agent", os.Getenv("FLEET_AGENT"), "agent name (default $FLEET_AGENT)")
	waiting := fs.String("waiting-for", "", "what the agent waits for, for a prompt event")
	noStatus := fs.Bool("no-status", false, "record subagents only; the agent's ACP host owns its status")
	rest, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: fleet hook <event> [--agent NAME] [--waiting-for TEXT] [--no-status]\nevents: %s",
			strings.Join(status.HookEvents(), ", "))
	}
	ev := rest[0]
	p := hookPayload(os.Stdin)
	now := time.Now()
	sub := status.SubagentEvent(ev)
	s, ok := status.EventState(ev, *waiting, now)
	if !ok && !sub {
		logx.For("status").Debug("unknown hook event", "event", ev, "agent", *agent)
		return nil
	}
	if *agent == "" {
		return fmt.Errorf("no agent name: pass --agent or set FLEET_AGENT")
	}
	if err := status.RecordSubs(f.Home, *agent, ev, p, now); err != nil {
		logx.For("status").Warn("hook subagents", "agent", *agent, "err", err)
	}
	if status.StopEvent(ev) && p.AgentID == "" && hookBlockStop(f, *agent, p, now) {
		_, _ = hookOut.Write(status.StopBlockJSON(board.StopReason))
		return nil // the turn goes on: no idle to record
	}
	if sub || *noStatus || p.AgentID != "" {
		return nil
	}
	if s.WaitingFor == "" {
		s.WaitingFor = p.WaitingFor(ev)
	}
	if err := status.WriteState(status.StateDir(f.Home), *agent, s); err != nil {
		logx.For("status").Warn("hook state", "agent", *agent, "err", err)
		return nil
	}
	return nil
}

// hookOut is where a hook's decision goes: the tool reads its stdout.
var hookOut io.Writer = os.Stdout

// hookBlockStop decides whether a Stop/agentStop hook blocks the stop because
// the agent owes a report (ADR-105), and records the block so the next stop
// for the same prompt is let through. Anything it can't read or write means
// allow: a hook must never trap an agent in a loop. It runs under
// --no-status too, since only the status write belongs to the ACP host.
func hookBlockStop(f *fleet.Fleet, agent string, p status.Payload, now time.Time) bool {
	a, ok := f.Store.Load().Agents[agent]
	if !ok || p.StopHookActive {
		return false
	}
	at, err := status.StopBlockedAt(f.Home, agent)
	if err != nil {
		logx.For("status").Warn("stop block marker", "agent", agent, "err", err)
		return false
	}
	if !a.BlockStop(now, p.StopHookActive, at) {
		return false
	}
	if err := status.MarkStopBlocked(f.Home, agent, now); err != nil {
		logx.For("status").Warn("stop block marker", "agent", agent, "err", err)
		return false
	}
	logx.For("status").Info("stop blocked: no report", "agent", agent)
	return true
}

// hookPayload reads a tool's hook JSON from r. A terminal stdin is left
// alone so the command does not hang when it is run by hand.
func hookPayload(r *os.File) status.Payload {
	fi, err := r.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice != 0 {
		return status.Payload{}
	}
	b, err := io.ReadAll(io.LimitReader(r, hookStdinMax))
	if err != nil || len(b) == 0 {
		return status.Payload{}
	}
	return status.ParsePayload(b)
}
