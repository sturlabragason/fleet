package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/graph"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// A repo with a built code graph hands it to the agent: GRAPHIFY_OUT in the
// pane env and one line in the playbook; without a graph, neither (DESIGN §6.15).
func TestSpawnCodeGraph(t *testing.T) {
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	argv, envOut := filepath.Join(home, "argv.txt"), filepath.Join(home, "env.txt")
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[pa]
command = "sh"
args = ["-c", "printf '%s' \"$1\" > `+argv+`; printf '%s' \"$GRAPHIFY_OUT\" > `+envOut+`; cat", "sh"]
prompt_args = ["{prompt}"]
patterns = "fake"
`), 0o644)
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := filepath.EvalSymlinks(t.TempDir())
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "x"}} {
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	no := false
	spawn := func(name string) (string, string) {
		os.Remove(argv)
		os.Remove(envOut)
		if _, err := f.SpawnWith(SpawnOpts{Worktree: &no}, name, "pa", repo, "fix it"); err != nil {
			t.Fatal(err)
		}
		a := readArg(t, argv)
		b, _ := os.ReadFile(envOut)
		return a, string(b)
	}

	prompt, env := spawn("g1")
	if env != "" || strings.Contains(prompt, "A code graph of this repo") {
		t.Fatalf("no graph, but env=%q prompt has line=%v", env, strings.Contains(prompt, "A code graph"))
	}

	out := graph.Dir(home, repo)
	os.MkdirAll(out, 0o755)
	os.WriteFile(filepath.Join(out, "graph.json"), nil, 0o644)
	os.WriteFile(filepath.Join(out, "fleet-graph.json"), []byte(`{"root":"`+repo+`","built_at":"2026-01-02T03:04:05Z","schema_version":1}`), 0o644)
	prompt, env = spawn("g2")
	if env != out {
		t.Errorf("GRAPHIFY_OUT = %q, want %q", env, out)
	}
	if !strings.Contains(prompt, "A code graph of this repo") || !strings.Contains(prompt, "$GRAPHIFY_OUT") {
		t.Errorf("playbook lacks the graph line:\n%.300q", prompt)
	}
}
