package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/tools"
)

// modelFleet is a fleet with the built-in profiles plus tools, under config
// (FLEET_CONFIG pinned to a temp file).
func modelFleet(t *testing.T, toolsTOML, config string) *Fleet {
	t.Helper()
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(toolsTOML), 0o644)
	cfg := filepath.Join(home, "config.toml")
	os.WriteFile(cfg, []byte(config), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// ADR-100: every effort and a leader run Opus. A named model must be Opus,
// a profile that names a model must name Opus, a profile with a model flag
// and no model is pinned to its default_model, and a tool with no model
// flag keeps its own default.
func TestPickModel(t *testing.T) {
	cases := []struct {
		name, tools, config, profile, named, want string
		refused                                   bool
	}{
		{"claude keeps its opus", "", "", "claude", "", tools.ClaudeModel, false},
		{"claude-copilot keeps its opus", "", "", "claude-copilot", "", tools.CopilotClaudeModel, false},
		{"copilot pinned to opus", "", "", "copilot", "", tools.CopilotClaudeModel, false},
		{"user copilot profile pinned", "[cp-work]\ncommand = \"copilot\"\n", "", "cp-work", "", tools.CopilotClaudeModel, false},
		{"named opus", "", "", "claude", "claude-opus-4.8", "claude-opus-4.8", false},
		{"named sonnet refused", "", "", "claude", "sonnet", "", true},
		{"named haiku refused", "", "", "copilot", "claude-haiku-5.5", "", true},
		{"no model flag", "", "", "vibe", "", "", false},
		{"no model flag refuses a named one", "", "", "vibe", "opus", "", true},
		{"profile names sonnet", "[cheap]\ncommand = \"claude\"\nargs = [\"--model\", \"sonnet\"]\n", "", "cheap", "", "", true},
		{"extra_args pin opus", "", "[profile.copilot]\nextra_args = [\"--model\", \"claude-opus-4.8\"]\n", "copilot", "", "claude-opus-4.8", false},
		{"extra_args pin gpt refused", "", "[profile.copilot]\nextra_args = [\"--model=gpt-6-sol\"]\n", "copilot", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := modelFleet(t, c.tools, c.config)
			got, err := f.pickModel(c.profile, f.Profiles[c.profile], c.named)
			if (err != nil) != c.refused || got != c.want {
				t.Errorf("pickModel = %q, %v; want %q, refused %v", got, err, c.want, c.refused)
			}
		})
	}
}

// Every effort and a leader launch the same Opus model; the pinned model
// replaces none the profile names, is recorded on the board, shown in the
// spawn label and comes back on wake. A non-Opus model is refused before
// anything starts.
func TestSpawnModelEveryEffort(t *testing.T) {
	f, _ := parkFleet(t, "[defaults]\nmax_working = 0\n")
	os.WriteFile(filepath.Join(f.Home, "tools.toml"), []byte(`[mdl]
command = "sh"
args = ["-c", "echo argv=$*; echo '\u2714 Done'; cat", "sh"]
model_args = ["--model", "{model}"]
default_model = "big-opus"
launch_id = ["started"]
resume = ["resumed"]
patterns = "fake"
[own]
command = "sh"
args = ["-c", "echo argv=$*; cat", "sh", "--model", "opus-own"]
model_args = ["--model", "{model}"]
default_model = "big-opus"
patterns = "fake"
`), 0o644)
	ps, err := tools.Load(f.Home)
	if err != nil {
		t.Fatal(err)
	}
	f.Profiles = ps
	for i, o := range []SpawnOpts{{Effort: "light"}, {Effort: "standard"}, {Effort: "pro"}, {Role: Role{Leader: true}}} {
		name := fmt.Sprintf("m%d", i)
		if _, err := f.SpawnWith(o, name, "mdl", f.Home, ""); err != nil {
			t.Fatal(err)
		}
		waitFor(t, f, name, "argv=--model big-opus started")
	}
	if got := f.SpawnLabel(f.Store.Load().Agents["m0"]); got != "mdl, model big-opus" {
		t.Errorf("label %q", got)
	}
	setState(t, f, "m0", "idle", time.Now())
	if err := f.Park("m0", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Wake("m0", false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, f, "m0", "argv=--model big-opus resumed")
	if _, err := f.SpawnWith(SpawnOpts{Effort: "light", Model: "claude-opus-4.8"}, "n1", "own", f.Home, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, f, "n1", "argv=--model claude-opus-4.8")
	if _, err := f.SpawnWith(SpawnOpts{Effort: "light"}, "n2", "own", f.Home, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, f, "n2", "argv=--model opus-own")
	if _, err := f.SpawnWith(SpawnOpts{Effort: "light", Model: "sonnet"}, "n3", "mdl", f.Home, ""); err == nil {
		t.Fatal("a sonnet spawn was accepted")
	}
	if _, ok := f.Store.Load().Agents["n3"]; ok {
		t.Error("a refused model still put the agent on the board")
	}
}

// A queued agent keeps the model its spawn named for the drain.
func TestQueuedAgentKeepsModel(t *testing.T) {
	f, _ := argvFleet(t, "[defaults]\nmax_working = 1\n")
	f.Profiles["pm"] = tools.Profile{Name: "pm", Command: "sh", Args: []string{"-c", "cat"}, Patterns: "fake",
		ModelArgs: []string{"--model", "{model}"}, DefaultModel: "opus"}
	if _, err := f.Spawn("q1", "pa", t.TempDir(), "first"); err != nil {
		t.Fatal(err)
	}
	q, err := f.SpawnWith(SpawnOpts{Model: "claude-opus-4.8"}, "q2", "pm", t.TempDir(), "second")
	if err != nil {
		t.Fatal(err)
	}
	if q.Status != "queued" || q.Model != "claude-opus-4.8" || f.SpawnLabel(q) != "pm, model claude-opus-4.8" {
		t.Fatalf("queued entry: status %q model %q label %q", q.Status, q.Model, f.SpawnLabel(q))
	}
}
