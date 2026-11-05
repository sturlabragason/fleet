package mcp

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/wall"
)

// fleet_status carries a spend column per agent and one fleet-wide total,
// read from the agent's own session file (ADR-46).
func TestStatusSpendColumnAndTotal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(fmt.Sprintf(`[cop]
command = "sh"
args = ["-c", "cat"]
launch_id = ["{id}"]
subagent_log = "%s/{id}.jsonl"
patterns = "copilot"
`, home)), 0o644)
	f, err := fleet.New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.Spawn("one", "cop", home, "")
	if err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(filepath.Join("..", "spend", "testdata", "copilot-events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(home, a.SessionID+".jsonl"), log, 0o644)

	s := New(f, wall.New(tc, fleet.Session))
	s.Fill = func() (int, int, bool) { return 0, 0, false }
	st, isErr := callText(t, s, "fleet_status", nil)
	if isErr {
		t.Fatalf("fleet_status: %s", st)
	}
	if !regexp.MustCompile(`(?m)^one \|.*\| 12\.8 AIC \|`).MatchString(st) {
		t.Errorf("spend column: %q", st)
	}
	if !strings.Contains(st, "fleet spend so far: 12.8 AIC over 3 model calls (61k tokens)") {
		t.Errorf("fleet total: %q", st)
	}
	if !strings.Contains(st, "not attributable") {
		t.Errorf("summariser note missing: %q", st)
	}
}
