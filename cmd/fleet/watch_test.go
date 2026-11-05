package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
