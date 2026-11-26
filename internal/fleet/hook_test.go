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
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// hookFleet starts one agent whose pane sits quietly at a shell prompt, so
// the screen says nothing and only a hook can move its status.
func hookFleet(t *testing.T) (*Fleet, *tmux.Client) {
	t.Helper()
	home := t.TempDir()
	noPlaybook(t, home)
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[quiet]
command = "sh"
args = ["-c", "cat"]
patterns = "fake"
`), 0o644)
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Spawn("h1", "quiet", home, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	return f, tc
}

func paneOption(t *testing.T, tc *tmux.Client, pane, key string) string {
	t.Helper()
	out, err := tc.Run("show-options", "-pqv", "-t", pane, key)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// A hook file decides the status when the screen says nothing fleet
// recognises, and the same transition reaches the pane border, so the border
// title and board.json never disagree (ADR-49).
func TestHookDrivesStatusAndBorder(t *testing.T) {
	f, tc := hookFleet(t)
	dir := status.StateDir(f.Home)
	st, _ := status.EventState("notification", "Claude needs your permission to use Bash", time.Now())
	if err := status.WriteState(dir, "h1", st); err != nil {
		t.Fatal(err)
	}
	as, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if len(as) != 1 || as[0].Status != string(status.Waiting) {
		t.Fatalf("hook did not set the status: %+v", as)
	}
	if !strings.Contains(as[0].LastLine, "permission to use Bash") {
		t.Errorf("waiting_for: %q", as[0].LastLine)
	}
	a := f.Store.Load().Agents["h1"]
	if got := paneOption(t, tc, a.PaneID, "@fleet_status"); got != "waiting" {
		t.Errorf("border title says %q, board says %q", got, a.Status)
	}
	// The next hook moves both again.
	done, _ := status.EventState("stop", "", time.Now())
	status.WriteState(dir, "h1", done)
	as, _ = f.Refresh()
	if as[0].Status != string(status.Idle) {
		t.Fatalf("stop hook: %+v", as[0])
	}
	if got := paneOption(t, tc, a.PaneID, "@fleet_status"); got != "idle" {
		t.Errorf("border title stale: %q", got)
	}
	// Closing the agent forgets its hook state, so a new agent of the same
	// name does not inherit it.
	f.Close("h1")
	if _, ok := status.ReadState(dir, "h1"); ok {
		t.Error("hook state survived close")
	}
}

// Events decide working and idle; the screen keeps menus, prompts and
// errors; a waiting hook yields to a pane that changed after it; a stale
// working hook and no hook at all leave the screen in charge (ADR-205).
// Screens are real Claude captures, fed through the capture cache so no
// tmux server is needed.
func TestHookPrecedence(t *testing.T) {
	const (
		none   = ""
		before = -time.Minute                  // fired before the pane last changed
		after  = time.Second                   // fired after it
		stale  = -HookWorkingMax - time.Minute // a working hook past its bound
	)
	for _, c := range []struct {
		name, screen, event string
		at                  time.Duration
		want                status.Status
	}{
		{"event beats screen: stop under a spinner", "working.txt", "stop", before, status.Idle},
		{"event beats screen: tool call at a quiet prompt", "idle.txt", "pretooluse", before, status.Working},
		{"event beats screen: newer hook", "working.txt", "stop", after, status.Idle},
		{"menu beats event: trust dialog", "waiting-trust.txt", "pretooluse", after, status.Waiting},
		{"menu beats event: approval", "waiting-approval.txt", "stop", after, status.Waiting},
		{"screen error beats event", "error.txt", "pretooluse", after, status.Error},
		{"waiting hook yields to a newer pane", "idle.txt", "notification", before, status.Idle},
		{"newer waiting hook stands", "idle.txt", "notification", after, status.Waiting},
		{"stale working hook falls back", "idle.txt", "pretooluse", stale, status.Idle},
		{"no event uses screen: working", "working.txt", none, 0, status.Working},
		{"no event uses screen: idle", "idle.txt", none, 0, status.Idle},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join("..", "status", "testdata", "claude", c.screen))
			if err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
			f, err := New(&tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}, home, "/nonexistent")
			if err != nil {
				t.Fatal(err)
			}
			f.caps.by = map[string]capEntry{"%1": {key: "k", text: string(b)}}
			a := &board.Agent{Name: "h1", Patterns: "claude", Status: string(status.Unknown)}
			start := time.Now().Add(-10 * time.Second)
			f.classify(a, true, false, "%1", "k", start) // the pane last changed 10s ago
			if c.event != none {
				hs, _ := status.EventState(c.event, "", a.LastChange.Add(c.at))
				if c.at == stale {
					hs.At = time.Now().Add(stale)
				}
				if err := status.WriteState(status.StateDir(home), "h1", hs); err != nil {
					t.Fatal(err)
				}
			}
			if got := f.classify(a, true, false, "%1", "k", start.Add(time.Second)); got != string(c.want) {
				t.Errorf("status %q, want %q", got, c.want)
			}
		})
	}
}

// The watcher stats the hook directory plus each running agent's session log.

// A tool killed in the middle of a tool call leaves its last "working" hook
// behind; after HookWorkingMax the screen decides again, so nothing sits at
// working forever (ADR-49).
func TestHookWorkingExpires(t *testing.T) {
	f, _ := hookFleet(t)
	dir := status.StateDir(f.Home)
	old := time.Now().Add(-HookWorkingMax - time.Minute)
	// An idle hook of that age still stands: idle is a resting state.
	idle, _ := status.EventState("stop", "", old)
	if err := status.WriteState(dir, "h1", idle); err != nil {
		t.Fatal(err)
	}
	as, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if as[0].Status != string(status.Idle) {
		t.Fatalf("old idle hook dropped: %q", as[0].Status)
	}
	// A working hook of the same age does not: it is the one state a killed
	// tool leaves behind, so the screen decides again.
	work, _ := status.EventState("pretooluse", "Bash", old)
	status.WriteState(dir, "h1", work)
	as, _ = f.Refresh()
	if as[0].Status == string(status.Working) {
		t.Errorf("a %v old working hook still holds the status", HookWorkingMax)
	}
}
