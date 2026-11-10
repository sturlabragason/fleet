package fleet

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/acp"
	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/tools"
)

// buildFleet builds the real fleet binary (acp-host and fakeagent included).
func buildFleet(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "fleet")
	out, err := exec.Command("go", "build", "-o", exe, "github.com/sturlabragason/fleet/cmd/fleet").CombinedOutput()
	if err != nil {
		t.Fatalf("build fleet: %v\n%s", err, out)
	}
	return exe
}

// waitStatus refreshes until name has status want, and returns its board row.
func waitStatus(t *testing.T, f *Fleet, name, want string, d time.Duration) *board.Agent {
	t.Helper()
	deadline := time.Now().Add(d)
	var a *board.Agent
	for {
		if _, err := f.Refresh(); err != nil {
			t.Fatal(err)
		}
		a = f.Store.Load().Agents[name]
		if a != nil && a.Status == want {
			return a
		}
		if time.Now().After(deadline) {
			got := "gone"
			if a != nil {
				got = fmt.Sprintf("%q (%s)", a.Status, a.LastLine)
			}
			b, _ := os.ReadFile(acphost.TranscriptPath(f.Home, name))
			t.Fatalf("%s: status %s, want %q\ntranscript:\n%s", name, got, want, b)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestACPEndToEnd runs the fake agent over ACP under the real fleet acp-host
// in a fleet-test-* tmux server: spawn, a permission question, answer, park,
// wake on the recorded session, a second prompt, close, and a crash.
func TestACPEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the fleet binary")
	}
	log := tmuxLog(t)
	home := t.TempDir()
	noPlaybook(t, home)
	t.Setenv("FLEET_HOME", home)
	t.Setenv("FAKE_ACP_STATE", filepath.Join(home, "fake-state"))
	exe := buildFleet(t)
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := New(tc, home, exe)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	wt := false

	a, err := f.SpawnWith(SpawnOpts{Driver: "acp", Worktree: &wt}, "a1", "fake", dir, "fix the bug [slow 300] [ask]")
	if err != nil {
		t.Fatal(err)
	}
	if !a.ACP() {
		t.Fatalf("driver %q", a.Driver)
	}
	waitStatus(t, f, "a1", "working", 10*time.Second)
	w := waitStatus(t, f, "a1", "waiting", 15*time.Second)
	if !strings.Contains(w.LastLine, "Allow") || !strings.Contains(w.LastLine, "Reject") {
		t.Errorf("waiting line %q lacks Allow/Reject", w.LastLine)
	}
	if c, err := f.Answer("a1", "y", ""); err != nil || !strings.Contains(c.Label, "Allow") {
		t.Fatalf("answer y: %+v %v", c, err)
	}
	waitStatus(t, f, "a1", "working", 10*time.Second)
	idle := waitStatus(t, f, "a1", "idle", 20*time.Second)
	sid := idle.SessionID
	if sid == "" {
		t.Fatal("no session id from the transcript")
	}
	if n := captures(t, log, a.PaneID); n != 0 {
		t.Errorf("the acp pane was captured %d times", n)
	}

	if err := f.Park("a1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := acphost.Call(home, "a1", acphost.Request{Op: acphost.OpState}, time.Second); err == nil {
		t.Error("host still answers after park")
	}
	woke, err := f.Wake("a1", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := startCommand(t, tc, woke.PaneID); !strings.Contains(got, "--session "+sid) || strings.Contains(got, "--prompt-file") {
		t.Errorf("wake command %q: want --session %s and no prompt file (it was sent)", got, sid)
	}
	if s := waitStatus(t, f, "a1", "idle", 15*time.Second).SessionID; s != sid {
		t.Errorf("session after wake %q want %q", s, sid)
	}
	if err := f.Send("a1", "one more thing [slow 800] [invoke]"); err != nil {
		t.Fatal(err)
	}
	// Working first, so the idle after it is this turn's, not the last one's.
	waitStatus(t, f, "a1", "working", 10*time.Second)
	// agy-style subagent: a dot from the transcript while it runs.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := f.Refresh(); err != nil {
			t.Fatal(err)
		}
		if s := f.Store.Load().Agents["a1"].Subagents; status.RunningCount(s) == 1 && s[0].Name == "helper" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no running subagent dot: %+v", f.Store.Load().Agents["a1"].Subagents)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if s := waitStatus(t, f, "a1", "idle", 15*time.Second).Subagents; status.RunningCount(s) != 0 {
		t.Errorf("subagent still running after the turn: %+v", s)
	}
	// Spend from the usage the host recorded: the spawn prompt's turn and this one.
	if sp, ok := f.Spend("a1"); !ok || sp.Calls != 2 || sp.Input != 200 || sp.Output != 20 {
		t.Errorf("spend %+v %v", sp, ok)
	}
	if p, _ := f.Peek("a1", 0); !strings.Contains(p, "one more thing") {
		t.Errorf("transcript lacks the second prompt:\n%s", p)
	}
	if n := captures(t, log, woke.PaneID); n != 0 {
		t.Errorf("the woken acp pane was captured %d times", n)
	}
	if err := f.Close("a1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Store.Load().Agents["a1"]; ok {
		t.Error("a1 still on the board after close")
	}

	// Adopt the closed agent's session under the acp driver: a new host
	// loads or resumes it, and it takes prompts like any acp agent.
	ad, err := f.Adopt(AdoptOpts{Profile: "fake", SessionID: sid, Dir: dir, Name: "ad1", Driver: "acp"})
	if err != nil {
		t.Fatal(err)
	}
	if !ad.ACP() || ad.SessionID != sid {
		t.Fatalf("adopted: driver %q session %q want acp %s", ad.Driver, ad.SessionID, sid)
	}
	if got := startCommand(t, tc, ad.PaneID); !strings.Contains(got, "acp-host ad1") || !strings.Contains(got, "--session "+sid) {
		t.Errorf("adopt command %q", got)
	}
	if s := waitStatus(t, f, "ad1", "idle", 15*time.Second).SessionID; s != sid {
		t.Errorf("adopted session %q want %q", s, sid)
	}
	if err := f.Send("ad1", "after adopt [slow 300]"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, f, "ad1", "working", 10*time.Second)
	waitStatus(t, f, "ad1", "idle", 15*time.Second)
	if n := captures(t, log, ad.PaneID); n != 0 {
		t.Errorf("the adopted acp pane was captured %d times", n)
	}
	if err := f.Close("ad1"); err != nil {
		t.Fatal(err)
	}

	if _, err := f.SpawnWith(SpawnOpts{Driver: "acp", Worktree: &wt}, "c1", "fake", dir, "boom [crash]"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, f, "c1", "error", 20*time.Second)
}

// fakeSession reads the fake ACP agent's record of session id.
func fakeSession(t *testing.T, home, id string) (s struct {
	Mode string              `json:"mode"`
	MCP  []acp.MCPServer     `json:"mcp"`
	Env  map[string]string   `json:"env"`
	Hist []map[string]string `json:"history"`
}) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "fake-state", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestClaudeACPEndToEnd runs a claude-shaped profile (the built-in claude's
// acp keys through acp_from, the fake agent as its adapter) under the real
// acp-host (ADR-76): the session mode, peer MCP over the session, the
// installed claude in CLAUDE_CODE_EXECUTABLE and no inherited API key or
// token, through spawn, a question, cancel, park and wake.
func TestClaudeACPEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the fleet binary")
	}
	tmuxLog(t)
	home := t.TempDir()
	cfg := filepath.Join(home, "config.toml")
	os.WriteFile(cfg, []byte("[defaults]\nplaybook = false\nauto_trust = false\nmode = \"auto\"\n[profile.fakeclaude]\ndriver = \"acp\"\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	t.Setenv("FLEET_HOME", home)
	t.Setenv("FAKE_ACP_STATE", filepath.Join(home, "fake-state"))
	t.Setenv("FAKE_ACP_ENV", "CLAUDE_CODE_EXECUTABLE,ANTHROPIC_MODEL,ANTHROPIC_API_KEY,CLAUDE_CODE_OAUTH_TOKEN,CLAUDE_CONFIG_DIR")
	t.Setenv("ANTHROPIC_API_KEY", "sk-must-not-pass")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "must-not-pass")
	bin := t.TempDir()
	claude := filepath.Join(bin, "claude")
	os.WriteFile(claude, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[fakeclaude]
command = "claude"
patterns = "fake"
env = { CLAUDE_CONFIG_DIR = "/x/.claude-personal" }
acp_from = "claude"
acp_command = ["{fleet}", "fakeagent", "{name}", "--acp", "--resume"]
`), 0o644)
	exe := buildFleet(t)
	t.Setenv("HOME", t.TempDir()) // after the build: no ~/.local/bin/claude ahead of the test's
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := New(tc, home, exe)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	wt := false

	a, err := f.SpawnWith(SpawnOpts{Worktree: &wt}, "c1", "fakeclaude", dir, "fix it [slow 300] [ask]")
	if err != nil {
		t.Fatal(err)
	}
	if !a.ACP() {
		t.Fatalf("[profile.fakeclaude] driver = acp: got %q", a.Driver)
	}
	waitStatus(t, f, "c1", "waiting", 15*time.Second)
	if c, err := f.Answer("c1", "y", ""); err != nil || !strings.Contains(c.Label, "Allow") {
		t.Fatalf("answer y: %+v %v", c, err)
	}
	sid := waitStatus(t, f, "c1", "idle", 20*time.Second).SessionID
	s := fakeSession(t, home, sid)
	if s.Mode != "bypassPermissions" {
		t.Errorf("session mode %q want bypassPermissions", s.Mode)
	}
	if len(s.MCP) != 1 || s.MCP[0].Name != "fleet-peer" || s.MCP[0].Command != exe ||
		strings.Join(s.MCP[0].Args, " ") != "mcp --agent c1" {
		t.Errorf("session MCP %+v", s.MCP)
	}
	want := map[string]string{"CLAUDE_CODE_EXECUTABLE": claude, "ANTHROPIC_MODEL": "opus", "CLAUDE_CONFIG_DIR": "/x/.claude-personal"}
	if !reflect.DeepEqual(s.Env, want) {
		t.Errorf("adapter env %v want %v", s.Env, want)
	}

	// Cancel a running turn.
	if err := f.Send("c1", "long one [hang]"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, f, "c1", "working", 10*time.Second)
	if err := f.SendKeys("c1", "", []string{"Escape"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, f, "c1", "idle", 10*time.Second)

	// Park and wake: the same session, its mode set again (now plan), MCP again.
	if err := f.Park("c1", ""); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(cfg, []byte("[defaults]\nplaybook = false\nauto_trust = false\nmode = \"plan\"\n[profile.fakeclaude]\ndriver = \"acp\"\n"), 0o644)
	if f, err = New(tc, home, exe); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Wake("c1", true); err != nil {
		t.Fatal(err)
	}
	if got := waitStatus(t, f, "c1", "idle", 15*time.Second).SessionID; got != sid {
		t.Errorf("session after wake %q want %q", got, sid)
	}
	if err := f.Send("c1", "after wake [slow 200]"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, f, "c1", "working", 10*time.Second)
	waitStatus(t, f, "c1", "idle", 15*time.Second)
	s = fakeSession(t, home, sid)
	if s.Mode != "plan" || len(s.MCP) != 1 || !reflect.DeepEqual(s.Env, want) {
		t.Errorf("after wake: mode %q mcp %+v env %v", s.Mode, s.MCP, s.Env)
	}
	if n := len(s.Hist); n < 4 {
		t.Errorf("history after wake: %d entries", n)
	}
	if err := f.Close("c1"); err != nil {
		t.Fatal(err)
	}
}

// TestClaudeCopilotACPEndToEnd spawns the built-in claude-copilot profile
// with no driver anywhere (ADR-74 (i)): it runs under the real acp-host, and
// the copilot it starts gets its model, the peer MCP server and the
// autopilot mode's --allow-all. A copilot shim on PATH records that argv and
// runs the fake agent over ACP: question, answer, park, wake on the same
// session, close.
func TestClaudeCopilotACPEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the fleet binary")
	}
	tmuxLog(t)
	home := t.TempDir()
	cfg := filepath.Join(home, "config.toml")
	os.WriteFile(cfg, []byte("[defaults]\nplaybook = false\nauto_trust = false\n[profile.claude-copilot]\nmode = \"autopilot\"\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	t.Setenv("FLEET_HOME", home)
	t.Setenv("FAKE_ACP_STATE", filepath.Join(home, "fake-state"))
	exe := buildFleet(t)
	bin := t.TempDir()
	argsFile := filepath.Join(home, "copilot.args")
	os.WriteFile(filepath.Join(bin, "copilot"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> "+argsFile+"\necho --- >> "+argsFile+"\nexec "+exe+" fakeagent cc1 --acp\n"), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir()) // no ~/.local/bin/copilot ahead of the shim
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := New(tc, home, exe)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	wt := false

	a, err := f.SpawnWith(SpawnOpts{Worktree: &wt}, "cc1", "claude-copilot", dir, "fix it [slow 300] [ask]")
	if err != nil {
		t.Fatal(err)
	}
	if !a.ACP() {
		t.Fatalf("claude-copilot with no driver configured: board driver %q, want acp", a.Driver)
	}
	waitStatus(t, f, "cc1", "waiting", 15*time.Second)
	if c, err := f.Answer("cc1", "y", ""); err != nil || !strings.Contains(c.Label, "Allow") {
		t.Fatalf("answer y: %+v %v", c, err)
	}
	sid := waitStatus(t, f, "cc1", "idle", 20*time.Second).SessionID
	if sid == "" {
		t.Fatal("no session id after the first turn")
	}
	b, _ := os.ReadFile(argsFile)
	argv := strings.Split(strings.TrimSuffix(string(b), "---\n"), "\n")
	cp, _ := tools.PeerMCP(exe, "cc1")
	for _, want := range [][]string{{"--additional-mcp-config", cp}, {"--model", tools.CopilotClaudeModel}, {"--acp", "--allow-all"}} {
		if !containsRun(argv, want) {
			t.Errorf("copilot argv lacks %q:\n%q", want, argv)
		}
	}

	if err := f.Park("cc1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Wake("cc1", true); err != nil {
		t.Fatal(err)
	}
	if got := waitStatus(t, f, "cc1", "idle", 15*time.Second); got.SessionID != sid || !got.ACP() {
		t.Errorf("after wake: session %q driver %q, want %q acp", got.SessionID, got.Driver, sid)
	}
	if err := f.Close("cc1"); err != nil {
		t.Fatal(err)
	}
}

// containsRun reports whether run appears in s as consecutive elements.
func containsRun(s, run []string) bool {
	for i := 0; i+len(run) <= len(s); i++ {
		if reflect.DeepEqual(s[i:i+len(run)], run) {
			return true
		}
	}
	return false
}
