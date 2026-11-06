package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// spawnFleet is a fleet with a "cat" profile and no config file of its own.
func spawnFleet(t *testing.T) *fleet.Fleet {
	t.Helper()
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte("[cat]\ncommand = \"sh\"\nargs = [\"-c\", \"cat\"]\npatterns = \"fake\"\n"), 0o644)
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	f, err := fleet.New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// `fleet spawn` run from inside a leader's pane used to create a sibling
// agent: not in the leader's squad, not in its team, reporting to the
// admiral. It joins the leader's squad instead (ADR-58).
func TestCLISpawnFromALeaderJoinsItsSquad(t *testing.T) {
	f := spawnFleet(t)
	if _, err := f.SpawnWith(fleet.SpawnOpts{Role: fleet.Role{Leader: true}}, "lead", "cat", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	pane, err := f.Pane("lead")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_PANE", pane)
	t.Setenv("TMUX", f.T.SocketPath()+",1,0")

	if err := cmdSpawn(f, []string{"m1", "cat", t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	m, err := f.SquadMember("lead", "m1")
	if err != nil {
		t.Fatalf("not in the squad: %v", err)
	}
	if m.LeadBy != "lead" || m.Leader {
		t.Errorf("lead_by %q leader %v", m.LeadBy, m.Leader)
	}
	if f.ReportsTo("m1") != "lead" {
		t.Errorf("m1 reports to %q", f.ReportsTo("m1"))
	}
	// A leader's agent may not lead a squad of its own, and the CLI says so.
	if err := cmdSpawn(f, []string{"--leader", "m2", "cat", t.TempDir()}); err == nil ||
		!strings.Contains(err.Error(), "fleet_squad_spawn") {
		t.Errorf("--leader from a leader: %v", err)
	}
}

// Everyone else still gets a plain sibling: the route must key off the
// calling pane being a leader's, not off there being any leader at all.
func TestCLISpawnOutsideALeaderStaysASibling(t *testing.T) {
	f := spawnFleet(t)
	if _, err := f.SpawnWith(fleet.SpawnOpts{Role: fleet.Role{Leader: true}}, "lead", "cat", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	// From the user's own shell: no agent pane at all.
	os.Unsetenv("TMUX_PANE")
	os.Unsetenv("TMUX")
	if err := cmdSpawn(f, []string{"s1", "cat", t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	// From a plain agent's pane.
	pane, err := f.Pane("s1")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_PANE", pane)
	t.Setenv("TMUX", f.T.SocketPath()+",1,0")
	if err := cmdSpawn(f, []string{"s2", "cat", t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"s1", "s2"} {
		a := f.Store.Load().Agents[n]
		if a == nil || a.LeadBy != "" {
			t.Errorf("%s lead_by %q, want a sibling", n, a.LeadBy)
		}
		if to := f.ReportsTo(n); to != "" {
			t.Errorf("%s reports to %q, want the admiral", n, to)
		}
	}
}

// From a leader's pane the CLI acts only on the leader's own squad, as its
// MCP tools do, and never types into a member's prompt (ADR-59).
func TestCLIFromALeaderIsScopedToItsSquad(t *testing.T) {
	f := spawnFleet(t)
	if _, err := f.SpawnWith(fleet.SpawnOpts{Role: fleet.Role{Leader: true}}, "lead", "cat", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.SquadSpawn("lead", fleet.SpawnOpts{}, "m1", "cat", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Spawn("outsider", "cat", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	// From the user's shell nothing is scoped.
	os.Unsetenv("TMUX_PANE")
	os.Unsetenv("TMUX")
	if err := leaderScope(f, "close", []string{"outsider"}); err != nil {
		t.Fatalf("user's shell scoped: %v", err)
	}
	pane, _ := f.Pane("lead")
	t.Setenv("TMUX_PANE", pane)
	t.Setenv("TMUX", f.T.SocketPath()+",1,0")
	for _, c := range []string{"close", "kill", "send", "park", "wake"} {
		if err := leaderScope(f, c, []string{"outsider", "x"}); err == nil {
			t.Errorf("leader ran %s on an outsider", c)
		}
		if err := leaderScope(f, c, []string{"m1", "x"}); err != nil {
			t.Errorf("leader refused %s on its own member: %v", c, err)
		}
	}
	if err := leaderScope(f, "close", []string{"lead", "--squad"}); err == nil {
		t.Error("leader closed itself through the CLI")
	}
	f.Store.Update(func(b *board.Board) error { b.Agents["m1"].Status = string(status.Waiting); return nil })
	if err := leaderScope(f, "send", []string{"m1", "y"}); err == nil || !strings.Contains(err.Error(), "prompt") {
		t.Errorf("leader typed into a member's prompt: %v", err)
	}
}

// A squad member's `fleet spawn --leader` would start a top-level leader
// and step around max_depth (ADR-59).
func TestCLISpawnLeaderFromAMemberIsRefused(t *testing.T) {
	f := spawnFleet(t)
	if _, err := f.SpawnWith(fleet.SpawnOpts{Role: fleet.Role{Leader: true}}, "lead", "cat", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.SquadSpawn("lead", fleet.SpawnOpts{}, "m1", "cat", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	pane, _ := f.Pane("m1")
	t.Setenv("TMUX_PANE", pane)
	t.Setenv("TMUX", f.T.SocketPath()+",1,0")
	if err := cmdSpawn(f, []string{"--leader", "l2", "cat", t.TempDir()}); err == nil {
		t.Fatal("a squad member started a leader of its own")
	}
	if _, ok := f.Store.Load().Agents["l2"]; ok {
		t.Fatal("l2 is on the board")
	}
}
