package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/watch"
)

func hookTestFleet(t *testing.T) *fleet.Fleet {
	t.Helper()
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := fleet.New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// `fleet hook <event> --agent NAME` is what a tool's own hook runs; it writes
// the state file the watcher stats.
func TestCmdHook(t *testing.T) {
	f := hookTestFleet(t)
	if err := cmdHook(f, []string{"Notification", "--agent", "a1", "--waiting-for", "needs permission"}); err != nil {
		t.Fatal(err)
	}
	s, ok := status.ReadState(status.StateDir(f.Home), "a1")
	if !ok || s.Status != status.Waiting || s.WaitingFor != "needs permission" {
		t.Fatalf("state: %+v ok=%v", s, ok)
	}
	if time.Since(s.At) > time.Minute {
		t.Errorf("stamped %v", s.At)
	}
	// The agent name may come from the environment, which is how it reaches
	// a hook command inside the pane.
	t.Setenv("FLEET_AGENT", "a2")
	if err := cmdHook(f, []string{"stop"}); err != nil {
		t.Fatal(err)
	}
	if s, ok := status.ReadState(status.StateDir(f.Home), "a2"); !ok || s.Status != status.Idle {
		t.Fatalf("env agent: %+v ok=%v", s, ok)
	}
	// An event fleet does not know is ignored, not an error: a broken hook
	// must never break the agent.
	os.Unsetenv("FLEET_AGENT")
	if err := cmdHook(f, []string{"SomeFutureEvent", "--agent", "a3"}); err != nil {
		t.Errorf("unknown event: %v", err)
	}
	if _, ok := status.ReadState(status.StateDir(f.Home), "a3"); ok {
		t.Error("unknown event wrote a state")
	}
	if err := cmdHook(f, []string{}); err == nil {
		t.Error("no event should be a usage error")
	}
}

func TestHookPayload(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "in.json")
	os.WriteFile(p, []byte(`{"message":"  Claude needs your permission  ","other":1}`), 0o644)
	r, _ := os.Open(p)
	defer r.Close()
	if got := hookPayload(r); got != "Claude needs your permission" {
		t.Errorf("payload %q", got)
	}
	os.WriteFile(p, []byte("not json"), 0o644)
	r2, _ := os.Open(p)
	defer r2.Close()
	if got := hookPayload(r2); got != "" {
		t.Errorf("broken payload %q", got)
	}
}

// `fleet watch --once` refreshes and exits, and --stop on a fleet with no
// watcher says so instead of failing.
func TestCmdWatchOnceAndStop(t *testing.T) {
	f := hookTestFleet(t)
	if err := cmdWatch(f, []string{"--once"}); err != nil {
		t.Fatalf("watch --once: %v", err)
	}
	if watch.Live(f.Home, time.Now()) {
		t.Error("--once left a heartbeat")
	}
	if err := cmdWatch(f, []string{"--stop"}); err != nil {
		t.Fatalf("watch --stop: %v", err)
	}
}

// The commands are reachable and documented.
func TestWatchInUsage(t *testing.T) {
	u := usage
	for _, s := range []string{"fleet watch", "fleet hook"} {
		if !strings.Contains(u, s) {
			t.Errorf("usage does not mention %q", s)
		}
	}
}

// fakeAdmiralFleet is a fleet with one agent and a scripted admiral pane:
// typed text lands in box, Enter moves it to submitted.
type fakeAdmiralFleet struct {
	f         *fleet.Fleet
	mu        sync.Mutex
	box       string
	submitted []string
	idle      bool
}

func newFakeAdmiralFleet(t *testing.T) *fakeAdmiralFleet {
	t.Helper()
	f := hookTestFleet(t)
	os.WriteFile(filepath.Join(f.Home, "tools.toml"),
		[]byte("[cat]\ncommand = \"sh\"\nargs = [\"-c\", \"cat\"]\npatterns = \"fake\"\n"), 0o644)
	f, err := fleet.New(f.T, f.Home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Spawn("a", "cat", f.Home, ""); err != nil {
		t.Fatal(err)
	}
	a := &fakeAdmiralFleet{f: f}
	f.SupPane = func() fleet.AdmiralView {
		a.mu.Lock()
		defer a.mu.Unlock()
		return fleet.AdmiralView{Pane: "%sup", Idle: a.idle, Box: true, Input: a.box}
	}
	f.TypeText = func(pane, text string) error {
		a.mu.Lock()
		a.box += text
		a.mu.Unlock()
		return nil
	}
	f.PressKeys = func(pane string, keys ...string) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, k := range keys {
			if k == "Enter" {
				a.submitted = append(a.submitted, a.box)
				a.box = ""
			}
		}
		return nil
	}
	return a
}

func (a *fakeAdmiralFleet) goIdle() { a.mu.Lock(); a.idle = true; a.mu.Unlock() }

func (a *fakeAdmiralFleet) lines() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.submitted...)
}

// A report that lands while the admiral is busy is typed by the watcher's
// poll once the pane goes idle: the watcher owns the once-a-poll
// housekeeping, so a wake is never left queued when no overview is open.
func TestCmdWatchDeliversQueuedWake(t *testing.T) {
	a := newFakeAdmiralFleet(t)
	f := a.f
	if _, err := f.Report("a", "blocked", "need creds"); err != nil {
		t.Fatal(err)
	}
	if len(a.lines()) != 0 {
		t.Fatalf("typed into a busy admiral: %q", a.lines())
	}
	done := make(chan error, 1)
	go func() { done <- cmdWatch(f, []string{"--interval", "100ms", "--tick", "50ms"}) }()
	t.Cleanup(func() {
		f.T.KillServer() // the watcher exits when the fleet session goes
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("watcher did not exit")
		}
	})
	a.goIdle()
	want := fleet.ReportLine("a", "blocked", "need creds")
	for i := 0; i < 100 && len(a.lines()) == 0; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if got := a.lines(); len(got) != 1 || got[0] != want {
		t.Fatalf("watcher did not deliver the queued wake: %q", got)
	}
}

// `fleet watch --once` is a refresh, not a resident poll: it must not
// auto-close agents, run somebody's done-check or wake the admiral.
func TestCmdWatchOnceDoesNoHousekeeping(t *testing.T) {
	a := newFakeAdmiralFleet(t)
	a.goIdle()
	if _, err := a.f.Report("a", "blocked", "need creds"); err != nil {
		t.Fatal(err)
	}
	if len(a.lines()) != 1 {
		t.Fatalf("the report itself did not wake the admiral: %q", a.lines())
	}
	if err := cmdWatch(a.f, []string{"--once"}); err != nil {
		t.Fatal(err)
	}
	if got := a.lines(); len(got) != 1 {
		t.Fatalf("--once did housekeeping: %q", got)
	}
	// Nor may it claim a queued spawn: it exits before starting it, which
	// would leave the agent claimed on the board and never launched.
	if err := a.f.Store.Update(func(b *board.Board) error {
		b.Agents["q"] = &board.Agent{Name: "q", Profile: "cat", Dir: a.f.Home,
			Status: string(status.Queued), Created: time.Now(), StatusSince: time.Now()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := cmdWatch(a.f, []string{"--once"}); err != nil {
		t.Fatal(err)
	}
	if got := a.f.Store.Load().Agents["q"].Status; got != string(status.Queued) {
		t.Fatalf("--once drained the queue: agent q is %q", got)
	}
}

// The overview delivers wakes even on the polls where a live watcher means
// it reads board.json instead of capturing panes itself (ADR-51).
func TestOverviewDeliversWakeWithWatcherLive(t *testing.T) {
	a := newFakeAdmiralFleet(t)
	if _, err := a.f.Report("a", "blocked", "need creds"); err != nil {
		t.Fatal(err)
	}
	if err := watch.WriteBeat(a.f.Home, watch.Beat{PID: os.Getpid(), Started: time.Now(), At: time.Now(), OK: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if !watch.Live(a.f.Home, time.Now()) {
		t.Fatal("heartbeat not live")
	}
	a.goIdle()
	be := &backend{f: a.f}
	if _, err := be.Refresh(); err != nil {
		t.Fatal(err)
	}
	want := fleet.ReportLine("a", "blocked", "need creds")
	if got := a.lines(); len(got) != 1 || got[0] != want {
		t.Fatalf("overview did not deliver the wake: %q", got)
	}
}

// A rebuilt binary that does not answer `watch --probe` with ok is never
// exec'd into: the watcher keeps its old code (ADR-62).
func TestReexecWatcherProbes(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "fleet")
	os.WriteFile(bad, []byte("#!/bin/sh\necho nope\n"), 0o755)
	if err := reexecWatcher(bad); err == nil || !strings.Contains(err.Error(), "probe") {
		t.Fatalf("bad probe: %v", err)
	}
	if err := reexecWatcher(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing binary exec'd")
	}
	f := hookTestFleet(t)
	out := captureOut(t, func() error { return cmdWatch(f, []string{"--probe"}) })
	if strings.TrimSpace(out) != "ok" {
		t.Fatalf("--probe printed %q", out)
	}
}

// After a re-exec the new image starts with its own pid's fresh heartbeat on
// disk. It must take over, not exit as if another watcher ran (ADR-62).
func TestCmdWatchAfterReexecOwnBeat(t *testing.T) {
	f := hookTestFleet(t)
	now := time.Now()
	if err := watch.WriteBeat(f.Home, watch.Beat{PID: os.Getpid(), Started: now, At: now, OK: now}); err != nil {
		t.Fatal(err)
	}
	// No fleet session on the test socket, so the watcher stops at its first
	// lifecycle check; what matters is that it got that far.
	out := captureOut(t, func() error { return cmdWatch(f, []string{"--tick", "10ms"}) })
	if strings.Contains(out, "already running") {
		t.Fatalf("re-exec'd watcher saw itself as another watcher: %q", out)
	}
	if b, err := os.ReadFile(filepath.Join(f.Home, "watch.lock")); err != nil || strings.TrimSpace(string(b)) != fmt.Sprint(os.Getpid()) {
		t.Fatalf("watcher never claimed watch.lock: %q %v", b, err)
	}
}
