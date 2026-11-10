package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestClaudeACPLive runs a real claude-personal agent with Haiku through
// the pinned adapter (ADR-76): a permission question and its answer, a
// cancel, park and wake by session/resume, the peer MCP server over the
// session, and spend from the Claude session file. It spends a few cents,
// so it runs only with FLEET_CLAUDE_ACP_LIVE=1, claude and npx on PATH and a
// login in ~/.claude-personal. Run it after bumping tools.ClaudeACPVersion.
func TestClaudeACPLive(t *testing.T) {
	if os.Getenv("FLEET_CLAUDE_ACP_LIVE") != "1" {
		t.Skip("set FLEET_CLAUDE_ACP_LIVE=1 to spend a few cents of Haiku on claude-personal")
	}
	for _, c := range []string{"claude", "npx"} {
		if _, err := exec.LookPath(c); err != nil {
			t.Skipf("%s not installed", c)
		}
	}
	userHome, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(userHome, ".claude-personal")); err != nil {
		t.Skip("no ~/.claude-personal login")
	}
	home := t.TempDir()
	cfg := filepath.Join(home, "config.toml")
	if err := os.WriteFile(cfg, []byte("[defaults]\nplaybook = false\nauto_trust = false\n[profile.claude-personal]\ndriver = \"acp\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_CONFIG", cfg)
	t.Setenv("FLEET_HOME", home)
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[claude-personal]
command = "claude"
env = { CLAUDE_CONFIG_DIR = "$HOME/.claude-personal" }
session_path = "~/.claude-personal/projects/*/{id}.jsonl"
patterns = "claude"
acp_from = "claude"
acp_env = { CLAUDE_CODE_EXECUTABLE = "{command_path}", ANTHROPIC_MODEL = "haiku" }
`), 0o644)
	exe := buildFleet(t)
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := New(tc, home, exe)
	if err != nil {
		t.Fatal(err)
	}
	dir, wt := t.TempDir(), false
	const name = "live-claude"
	a, err := f.SpawnWith(SpawnOpts{Worktree: &wt}, name, "claude-personal", dir,
		"Use the Bash tool to run exactly: touch fleet-live.txt  Then reply with the single word DONE.")
	if err != nil {
		t.Fatal(err)
	}
	if !a.ACP() {
		t.Fatalf("driver %q, want acp from [profile.claude-personal]", a.Driver)
	}
	answer := func() {
		t.Helper()
		w := waitStatus(t, f, name, "waiting", 180*time.Second)
		t.Logf("question: %s", w.LastLine)
		if c, err := f.Answer(name, "y", ""); err != nil {
			t.Fatalf("answer: %+v %v", c, err)
		}
	}
	answer()
	idle := waitStatus(t, f, name, "idle", 180*time.Second)
	if _, err := os.Stat(filepath.Join(dir, "fleet-live.txt")); err != nil {
		t.Errorf("the allowed command did not run: %v", err)
	}
	sid := idle.SessionID

	if err := f.Send(name, "Write the numbers from 1 to 400, one per line, nothing else."); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, f, name, "working", 60*time.Second)
	time.Sleep(2 * time.Second)
	if err := f.SendKeys(name, "", []string{"Escape"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, f, name, "idle", 30*time.Second)

	if err := f.Park(name, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Wake(name, true); err != nil {
		t.Fatal(err)
	}
	if got := waitStatus(t, f, name, "idle", 180*time.Second).SessionID; got != sid {
		t.Errorf("session after wake %q want %q", got, sid)
	}
	if err := f.Send(name, "Call the fleet_agents tool of the fleet-peer MCP server once. Then reply with the name of the file you created earlier and nothing else."); err != nil {
		t.Fatal(err)
	}
	answer()
	waitStatus(t, f, name, "idle", 180*time.Second)
	p, _ := f.Peek(name, 0)
	t.Logf("transcript:\n%s", p)
	if !strings.Contains(p, "fleet-live.txt") {
		t.Error("the woken agent did not remember its file")
	}
	sp, ok := f.Spend(name)
	t.Logf("spend: %s (%+v, %v)", sp.Long(), sp, ok)
	if !ok || sp.Total() == 0 {
		t.Error("no spend read from the Claude session file")
	}
	if err := f.Close(name); err != nil {
		t.Fatal(err)
	}
}
