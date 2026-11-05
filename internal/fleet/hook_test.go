package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// A hook older than the pane's last change loses to the screen; a newer one
// wins.
func TestHookPrecedence(t *testing.T) {
	f, _ := hookFleet(t)
	a := f.Store.Load().Agents["h1"]
	a.LastChange = time.Now()
	old, _ := status.EventState("stop", "", a.LastChange.Add(-time.Minute))
	if err := status.WriteState(status.StateDir(f.Home), "h1", old); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.hookState(a, true); ok {
		t.Error("a stale hook beat a matching screen")
	}
	if _, ok := f.hookState(a, false); !ok {
		t.Error("a hook must decide when the screen says nothing")
	}
	fresh, _ := status.EventState("stop", "", a.LastChange.Add(time.Second))
	status.WriteState(status.StateDir(f.Home), "h1", fresh)
	if _, ok := f.hookState(a, true); !ok {
		t.Error("a hook fired after the pane changed must win")
	}
}

// The watcher stats the hook directory plus each running agent's session log.
func TestWatchPaths(t *testing.T) {
	f, _ := hookFleet(t)
	ps := f.WatchPaths()
	if len(ps) == 0 || ps[0] != status.StateDir(f.Home) {
		t.Fatalf("paths: %q", ps)
	}
	for _, p := range ps {
		if !filepath.IsAbs(p) {
			t.Errorf("relative watch path %q", p)
		}
	}
}

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
