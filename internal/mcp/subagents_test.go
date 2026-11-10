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

func TestStatusSubagentsColumn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(fmt.Sprintf(`[sub]
command = "sh"
args = ["-c", "echo working; cat"]
launch_id = ["{id}"]
subagent_log = "%s/{id}.jsonl"
patterns = "fake"
`, home)), 0o644)
	f, err := fleet.New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.Spawn("s1", "sub", home, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Spawn("s2", "sub", home, ""); err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(filepath.Join("..", "status", "testdata", "copilot-events", "background.jsonl"))
	os.WriteFile(filepath.Join(home, a.SessionID+".jsonl"), log, 0o644)
	s := New(f, wall.New(tc, fleet.Session))
	s.Fill = func() (int, int, bool) { return 0, 0, false }
	st, isErr := callText(t, s, "fleet_status", nil)
	if isErr || !strings.HasPrefix(st, "admiral: ?\nname | team | squad | profile | driver | status | subagents | wall | age | spend | waiting_for | report\n") {
		t.Fatalf("header: %q", st)
	}
	if !regexp.MustCompile(`(?m)^s1 \|  \|  \| sub \| tmux \| \w+ \| 1 \| `).MatchString(st) || !regexp.MustCompile(`(?m)^s2 \|  \|  \| sub \| tmux \| \w+ \|  \| `).MatchString(st) {
		t.Errorf("subagents column: %q", st)
	}
}
