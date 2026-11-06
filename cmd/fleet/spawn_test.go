package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/fleet"
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
