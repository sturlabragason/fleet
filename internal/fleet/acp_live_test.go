package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/tmux"
)

// TestACPDefaultLive spawns real copilot and agy agents with no --driver on
// a fleet-test-* server, so they run over ACP by default (ADR-74), and checks
// a one-word turn: working, idle, a session id, spend, close. It spends a few
// tokens, so it runs only with FLEET_ACP_LIVE=1 and the tool on PATH.
func TestACPDefaultLive(t *testing.T) {
	if os.Getenv("FLEET_ACP_LIVE") != "1" {
		t.Skip("set FLEET_ACP_LIVE=1 to spend a few tokens on real copilot and agy")
	}
	home := t.TempDir()
	cfg := filepath.Join(home, "config.toml")
	// No playbook (fewer tokens) and no writes to the tools' trust stores.
	if err := os.WriteFile(cfg, []byte("[defaults]\nplaybook = false\nauto_trust = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_CONFIG", cfg)
	t.Setenv("FLEET_HOME", home)
	exe := buildFleet(t)
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := New(tc, home, exe)
	if err != nil {
		t.Fatal(err)
	}
	wt := false
	for _, tool := range []string{"copilot", "agy"} {
		t.Run(tool, func(t *testing.T) {
			if _, err := exec.LookPath(f.Profiles[tool].Command); err != nil {
				t.Skipf("%s not installed", tool)
			}
			name := "live-" + tool
			a, err := f.SpawnWith(SpawnOpts{Worktree: &wt}, name, tool, t.TempDir(), "Reply with the single word OK and do nothing else.")
			if err != nil {
				t.Fatal(err)
			}
			if !a.ACP() {
				t.Fatalf("%s spawned with driver %q, want acp by default", tool, a.Driver)
			}
			waitStatus(t, f, name, "working", 60*time.Second)
			idle := waitStatus(t, f, name, "idle", 180*time.Second)
			if idle.SessionID == "" {
				t.Error("no session id")
			}
			p, _ := f.Peek(name, 0)
			t.Logf("%s transcript:\n%s", tool, p)
			sp, ok := f.Spend(name)
			t.Logf("%s spend: %s (%+v, %v)", tool, sp.Long(), sp, ok)
			if !ok || sp.Total() == 0 {
				t.Errorf("%s: no spend read", tool)
			}
			if err := f.Close(name); err != nil {
				t.Fatal(err)
			}
		})
	}
}
