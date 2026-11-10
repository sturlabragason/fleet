package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/tmux"
)

func TestSpawnLsSendClose(t *testing.T) {
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	// A profile whose pane prints a y/n prompt, using cat to echo input.
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[yn]
command = "sh"
args = ["-c", "printf 'Approve? [y/n] '; cat"]
patterns = "fake"
`), 0o644)
	// Keep the user's real ~/.config/fleet/config.toml out of the test.
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Spawn("a1", "yn", home, ""); err != nil {
		t.Fatal(err)
	}
	var st string
	for i := 0; i < 40; i++ {
		as, _ := f.Refresh()
		if len(as) == 1 && as[0].Status == "waiting" {
			st = as[0].Status
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if st != "waiting" {
		t.Fatalf("status never waiting")
	}
	if err := f.Send("a1", "hello"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if out, _ := f.Peek("a1", 0); !strings.Contains(out, "hello") {
		t.Errorf("peek: %q", out)
	}
	if err := f.Close("a1"); err != nil {
		t.Fatal(err)
	}
	if len(f.Store.Load().Agents) != 0 {
		t.Error("close left board entry")
	}
}

// TestSpawnPromptWaitsForReady covers the bug ADR-13 fixes: the pane only
// accepts input well after the old fixed 1500 ms sleep, so a timed paste lost
// the prompt. Spawn must wait for the pane to look ready instead.
//
// Note on the assertion: merely finding the prompt somewhere in the pane does
// NOT guard the bug. tmux buffers input written before the program reaches the
// terminal, so an early paste is still echoed by the pty — it just lands as a
// bare line *above* the input box, and the program then reads it back out of
// the buffer. The test therefore pins *where* the prompt first appears:
// directly after the "> " marker, which only happens when the paste waited.
func TestSpawnPromptWaitsForReady(t *testing.T) {
	home := t.TempDir()
	noPlaybook(t, home)
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	// No prompt_args, and the input box appears only after 2.5s.
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[late]
command = "sh"
args = ["-c", "sleep 2.5; printf '> '; cat"]
patterns = "fake"
`), 0o644)
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Spawn("late1", "late", home, "hello fleet"); err != nil {
		t.Fatal(err)
	}
	out, _ := f.Peek("late1", 0)
	i := strings.Index(out, "hello fleet")
	if i < 0 {
		t.Fatalf("prompt not delivered; pane:\n%s", out)
	}
	if i < 2 || out[i-2:i] != "> " {
		t.Fatalf("prompt did not land at the input box (pasted before ready); pane:\n%s", out)
	}
	if err := f.Close("late1"); err != nil {
		t.Fatal(err)
	}
}

// TestSpawnFakeAgentEndToEnd is the end-to-end check from AGENTS.md: the real
// `fake` profile (which deliberately has no prompt_args, so it exercises the
// ready-then-paste path) on a throwaway tmux server. Needs `make build`.
//
// As in TestSpawnPromptWaitsForReady, "the prompt is somewhere in the pane"
// proves nothing: tmux buffers an early paste and the pty echoes it anyway.
// The guard is that the echoed prompt appears *after* the fake agent's ready
// banner, which can only happen if the paste waited for the pane to be ready.
func TestSpawnFakeAgentEndToEnd(t *testing.T) {
	exe, err := filepath.Abs("../../bin/fleet")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(exe); err != nil {
		t.Skip("bin/fleet not built; run make build")
	}
	home := t.TempDir()
	noPlaybook(t, home)
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := New(tc, home, exe)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Spawn("a1", "fake", home, "hello fleet"); err != nil {
		t.Fatal(err)
	}
	var out string
	for i := 0; i < 30; i++ {
		out, _ = f.Peek("a1", 0)
		if strings.Contains(out, "queued: hello fleet") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(out, "queued: hello fleet") {
		t.Fatalf("fake agent never received the prompt; pane:\n%s", out)
	}
	banner := strings.Index(out, "ready (tool: fake)")
	first := strings.Index(out, "hello fleet")
	if banner < 0 || first < banner {
		t.Fatalf("prompt was typed before the agent was ready (banner=%d prompt=%d); pane:\n%s", banner, first, out)
	}
}

// A pane whose process exited is error, and the pane tag follows the board
// even when it drifted (e.g. left at "working" by an older fleet).
func TestDeadPaneIsErrorAndTagResyncs(t *testing.T) {
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[short]
command = "sh"
args = ["-c", "echo bye; sleep 0.5"]
patterns = "fake"
`), 0o644)
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Spawn("d1", "short", home, ""); err != nil {
		t.Fatal(err)
	}
	paneStatus := func() string {
		ps, _ := tc.ListPanes("")
		for _, p := range ps {
			if p.Agent == "d1" {
				return p.Status
			}
		}
		return ""
	}
	var as []*board.Agent
	for i := 0; i < 50; i++ {
		as, _ = f.Refresh()
		if len(as) == 1 && as[0].Status == "error" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(as) != 1 || as[0].Status != "error" {
		t.Fatalf("dead pane not error: %+v", as)
	}
	if got := paneStatus(); got != "error" {
		t.Fatalf("pane tag %q, want error", got)
	}
	ps, _ := tc.ListPanes("")
	for _, p := range ps {
		if p.Agent == "d1" {
			tc.SetPaneOption(p.ID, "@fleet_status", "working")
		}
	}
	f.Refresh()
	if got := paneStatus(); got != "error" {
		t.Errorf("pane tag not re-synced: %q", got)
	}
}

// Choose and Answer move the ❯ cursor with Up/Down from the selected item,
// then press Enter; Send presses Enter again for a paste left unsubmitted.
func TestChooseAndSubmit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "patterns.toml"), []byte("[pend]\npending = ['\\[Paste #\\d+']\nwaiting = ['\\[y/n\\]']\nidle = ['^> ']\n"), 0o644)
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[menu]
command = "sh"
args = ["-c", "stty -echo; printf 'Do you want to?\n  1. Yes\n❯ 2. Yes, more\n  3. No, stop\n'; cat -v"]
patterns = "fake"
[swallow]
command = "sh"
args = ["-c", "stty -echo; printf '> '; read a; printf '[Paste #1 - 1 lines]\n'; read b; printf '\033[2J\033[Hsubmitted %s\n' \"$a\"; cat"]
patterns = "pend"
[asks]
command = "sh"
args = ["-c", "stty -echo; printf 'Approve? [y/n]\n[Paste #1 - 1 lines]\n'; while read l; do echo got; done"]
patterns = "pend"
[stuck]
command = "sh"
args = ["-c", "stty -echo; printf '[Paste #1 - 1 lines]\n'; cat >/dev/null"]
patterns = "pend"
`), 0o644)
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	oldP, oldW := SubmitPause, SubmitWait
	SubmitPause, SubmitWait = 10*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { SubmitPause, SubmitWait = oldP, oldW })
	for n, p := range map[string]string{"m": "menu", "s": "swallow", "x": "stuck", "q": "asks"} {
		if _, err := f.Spawn(n, p, home, ""); err != nil {
			t.Fatal(err)
		}
	}
	waitFor := func(name, want string) string {
		var out string
		for i := 0; i < 40; i++ {
			if out, _ = f.Peek(name, 0); strings.Contains(out, want) {
				return out
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("%s never showed %q: %q", name, want, out)
		return ""
	}
	waitFor("m", "3. No, stop")
	if _, err := f.Choose("m", 1, "run this command"); err == nil || err.Error() != "prompt changed: now 'Do you want to?'" {
		t.Fatalf("expect mismatch: %v", err)
	}
	if c, err := f.Choose("m", 1, "do YOU want"); err != nil || c.Label != "1. Yes" || c.Question != "Do you want to?" {
		t.Fatalf("choose: %+v %v", c, err)
	}
	out := strings.ReplaceAll(waitFor("m", "^["), "^[O", "^[[") // either cursor key mode
	if !strings.HasSuffix(out, "3. No, stop\n^[[A") {
		t.Errorf("want one Up then Enter: %q", out)
	}
	if c, err := f.Answer("m", "n", "no, stop"); err != nil || c.Label != "3. No, stop" {
		t.Fatalf("answer n: %+v %v", c, err)
	}
	if out := strings.ReplaceAll(waitFor("m", "B"), "^[O", "^[["); !strings.HasSuffix(out, "\n^[[A\n^[[B") {
		t.Errorf("want one Down for n: %q", out)
	}
	if _, err := f.Choose("m", 5, ""); err == nil {
		t.Error("option 5 of 3 accepted")
	}
	if _, err := f.Choose("x", 1, ""); err == nil || !strings.Contains(err.Error(), "no prompt with options") {
		t.Errorf("choose without menu: %v", err)
	}
	if got := MenuKeys(3, 1); strings.Join(got, ",") != "Up,Up,Enter" {
		t.Errorf("menu keys %v", got)
	}

	waitFor("s", "> ")
	if err := f.Send("s", "hello"); err != nil {
		t.Fatalf("send with swallowed Enter: %v", err)
	}
	waitFor("s", "submitted hello")
	waitFor("x", "[Paste #1")
	if err := f.Send("x", "hi"); err == nil || !strings.Contains(err.Error(), "not submitted") {
		t.Errorf("stuck paste: %v", err)
	}
	// A prompt on screen: no retry Enter, which would answer it.
	waitFor("q", "[Paste #1")
	if err := f.Send("q", "hi"); err == nil || !strings.Contains(err.Error(), "not submitted") {
		t.Errorf("paste under a prompt: %v", err)
	}
	if out, _ := f.Peek("q", 0); strings.Count(out, "got") != 1 {
		t.Errorf("want exactly one Enter: %q", out)
	}
	if line, err := f.Prompting("x"); err != nil || line != "" {
		t.Errorf("unsubmitted input is no prompt: %q %v", line, err)
	}
	if line, _ := f.Prompting("m"); line == "" {
		t.Error("menu agent should be prompting")
	}
	if err := CheckKeys([]string{"Enter", "C-c"}); err == nil {
		t.Error("C-c allowed")
	}
}

func TestMode(t *testing.T) {
	for in, want := range map[string]string{
		" Autopilot · Allow All · ● Working · 32.6 KiB esc esc interrupt\nctx ██ 5%":        "Autopilot · Allow All",
		" ← open sidebar · Interactive · Manual Approval · @ files · space hold to record ": "Interactive · Manual Approval",
		"> ": "",
	} {
		if got := Mode(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

// noPlaybook pins FLEET_CONFIG to a file that turns the worker playbook off,
// for tests about where a pasted prompt lands: the multi-line playbook would
// scroll the pane and move the prompt away from the input box marker.
func noPlaybook(t *testing.T, home string) {
	t.Helper()
	cfg := filepath.Join(home, "config.toml")
	if err := os.WriteFile(cfg, []byte("[defaults]\nplaybook = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_CONFIG", cfg)
}

// A Claude agent's file tools are fenced to its <repo>.wt/ dir only when it
// has a fleet worktree and fence_writes is on (ADR-79).
func TestFenceDir(t *testing.T) {
	w := &board.Worktree{Path: "/src/repo.wt/a1"}
	if got := fenceDir(w, true); got != "/src/repo.wt" {
		t.Fatalf("got %q", got)
	}
	if fenceDir(w, false) != "" || fenceDir(nil, true) != "" {
		t.Fatal("fenced without a worktree or with fence_writes off")
	}
}
