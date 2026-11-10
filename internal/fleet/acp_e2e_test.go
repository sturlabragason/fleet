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

	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/tmux"
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
	if got := startCommand(t, tc, woke.PaneID); !strings.Contains(got, "--session "+sid) {
		t.Errorf("wake command %q lacks --session %s", got, sid)
	}
	if s := waitStatus(t, f, "a1", "idle", 15*time.Second).SessionID; s != sid {
		t.Errorf("session after wake %q want %q", s, sid)
	}
	if err := f.Send("a1", "one more thing [slow 300]"); err != nil {
		t.Fatal(err)
	}
	// Working first, so the idle after it is this turn's, not the last one's.
	waitStatus(t, f, "a1", "working", 10*time.Second)
	waitStatus(t, f, "a1", "idle", 15*time.Second)
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

	if _, err := f.SpawnWith(SpawnOpts{Driver: "acp", Worktree: &wt}, "c1", "fake", dir, "boom [crash]"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, f, "c1", "error", 20*time.Second)
}
