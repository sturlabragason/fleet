package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/status"
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *stop:
		return stopWatcher(f)
	case *once:
		_, err := f.Refresh()
		return err
	}
	if b, ok := watch.ReadBeat(f.Home); ok && watch.Live(f.Home, time.Now()) {
		fmt.Printf("a watcher is already running (pid %d, since %s)\n", b.PID, b.Started.Format(time.Kitchen))
		return nil
	}
	// The watcher captures every agent on every pass: with no overview open
	// there is no wall, so "off the wall" would mean every agent and status
	// would lag by OffWallEvery (DESIGN §7a).
	f.OffWallEvery = 0
	f.DrainQueue = true
	w := &watch.Watcher{
		Home:     f.Home,
		Refresh:  func() error { _, err := f.Refresh(); return err },
		Paths:    f.WatchPaths,
		Stop:     func() bool { return !f.T.HasSession(fleet.Session) },
		Interval: *interval,
		Tick:     *tick,
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return w.Run(ctx)
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

// startWatcher launches `fleet watch` detached, unless one is already live.
// It is best-effort: a fleet without a watcher still works, every command
// just refreshes for itself.
func startWatcher(f *fleet.Fleet) {
	log := logx.For("watch")
	// Running, not Live: a watcher whose refreshes are failing is still the
	// watcher, and a second one would only double the scraping.
	if pid, ok := watch.Running(f.Home); ok {
		log.Debug("watcher already running", "pid", pid)
		return
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

// hookStdinMax caps what a hook payload may contribute; tools send a small
// JSON object, and the point of reading it is not to block the tool.
const hookStdinMax = 64 << 10

// cmdHook records what an agent's own tool just did:
//
//	fleet hook <event> [--agent NAME] [--waiting-for TEXT]
//
// It writes ~/.fleet/status/<name>.json, which the watcher notices within a
// tick (DESIGN §18 #3, ADR-49). It is wired into Claude through the per-agent
// --settings fleet passes at spawn, so the user's own settings file is never
// touched. It never fails loudly: a broken hook must not break the agent.
func cmdHook(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	agent := fs.String("agent", os.Getenv("FLEET_AGENT"), "agent name (default $FLEET_AGENT)")
	waiting := fs.String("waiting-for", "", "what the agent waits for, for a prompt event")
	rest, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: fleet hook <event> [--agent NAME] [--waiting-for TEXT]\nevents: %s",
			strings.Join(status.HookEvents(), ", "))
	}
	msg := hookPayload(os.Stdin)
	if *waiting == "" {
		*waiting = msg
	}
	s, ok := status.EventState(rest[0], *waiting, time.Now())
	if !ok {
		logx.For("status").Debug("unknown hook event", "event", rest[0], "agent", *agent)
		return nil
	}
	if *agent == "" {
		return fmt.Errorf("no agent name: pass --agent or set FLEET_AGENT")
	}
	if err := status.WriteState(status.StateDir(f.Home), *agent, s); err != nil {
		logx.For("status").Warn("hook state", "agent", *agent, "err", err)
		return nil
	}
	return nil
}

// hookPayload reads a tool's hook JSON from r and returns its message, if
// any. A terminal stdin is left alone so the command does not hang when it
// is run by hand.
func hookPayload(r *os.File) string {
	fi, err := r.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice != 0 {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(r, hookStdinMax))
	if err != nil || len(b) == 0 {
		return ""
	}
	var p struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &p) != nil {
		return ""
	}
	return strings.TrimSpace(p.Message)
}
