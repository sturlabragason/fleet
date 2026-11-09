package mcp

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/wall"
)

// spawnHome is a fleet at max_working 1 with one working agent and one
// queued, so every spawn queues without starting a pane. cfg is appended to
// config.toml's [defaults].
func spawnHome(t *testing.T, cfg string) (*fleet.Fleet, *Server) {
	t.Helper()
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte("[m]\ncommand = \"sh\"\nargs = [\"--model\", \"opus-x\"]\npatterns = \"fake\"\n"), 0o644)
	p := filepath.Join(home, "config.toml")
	os.WriteFile(p, []byte("[defaults]\nmax_working = 1\n"+cfg), 0o644)
	t.Setenv("FLEET_CONFIG", p)
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := fleet.New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	f.FreeMem = func() (uint64, error) { return 64 << 30, nil }
	now := time.Now()
	f.Store.Update(func(b *board.Board) error {
		b.Agents["w"] = &board.Agent{Name: "w", Profile: "m", Dir: home, Status: "working", Created: now, StatusSince: now}
		b.Agents["q0"] = &board.Agent{Name: "q0", Profile: "m", Dir: home, Status: "queued", Created: now.Add(-time.Minute), StatusSince: now}
		return nil
	})
	return f, NewAdmiral(f, wall.New(f.T, fleet.Session), "")
}

// A spawn without a profile takes [defaults] profile, and a queued reply
// names the profile, its model and how many are ahead.
func TestSpawnDefaultProfileAndQueuedReply(t *testing.T) {
	f, s := spawnHome(t, "profile = \"m\"\n")
	msg, isErr := callText(t, s, "fleet_spawn", map[string]any{"name": "x", "dir": f.Home})
	if isErr {
		t.Fatal(msg)
	}
	want := "queued x (m, model opus-x) in " + f.Home + ": 1 ahead of it, 1 agents busy, max_working 1"
	if !strings.HasPrefix(msg, want) {
		t.Fatalf("reply %q\nwant prefix %q", msg, want)
	}
	if a := f.Store.Load().Agents["x"]; a == nil || a.Profile != "m" {
		t.Fatalf("board entry %+v", a)
	}
}

func TestSpawnWithoutProfileOrDefaultNamesTheFix(t *testing.T) {
	f, s := spawnHome(t, "")
	msg, isErr := callText(t, s, "fleet_spawn", map[string]any{"name": "x", "dir": f.Home})
	if !isErr || !strings.Contains(msg, "[defaults] profile") || !strings.Contains(msg, "fleet_profiles") {
		t.Fatalf("reply %q (isErr=%v)", msg, isErr)
	}
	// A profile with no --model says so.
	if got := f.SpawnLabel(&board.Agent{Profile: "fake"}); got != "fake, tool default model" {
		t.Errorf("label %q", got)
	}
}
