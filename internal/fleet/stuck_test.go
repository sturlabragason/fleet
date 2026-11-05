package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// stuckFleet spawns one agent whose pane prints a working screen and then
// goes quiet for good, with the given [defaults] config on top of
// playbook = false. It returns the fleet, the agent name and the panes keys
// were pressed into.
func stuckFleet(t *testing.T, cfg string) (*Fleet, *[]string) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte("[defaults]\nplaybook = false\nworktree = false\n"+cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_CONFIG", path)
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[frozen]
command = "sh"
args = ["-c", "printf '● Task: x\n⠹ working · 3s · (esc to interrupt)\n'; sleep 600"]
patterns = "fake"
`), 0o644)
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	// Only the overview reports stuck episodes (ADR-45); since ADR-44 the
	// MCP server and `fleet ls` drain the queue too, so the flag that says
	// "this is the overview" is Housekeeping.
	f.DrainQueue, f.Housekeeping = true, true
	var mu sync.Mutex
	var keyed []string
	f.PressKeys = func(pane string, keys ...string) error {
		mu.Lock()
		defer mu.Unlock()
		keyed = append(keyed, strings.Join(keys, " "))
		return nil
	}
	f.SupPane = func() AdmiralView { return AdmiralView{} } // never idle: no typing
	if _, err := f.Spawn("a1", "frozen", home, ""); err != nil {
		t.Fatal(err)
	}
	return f, &keyed
}

// refreshUntilStuck polls until a1 is stuck, then refreshes a few more times.
func refreshUntilStuck(t *testing.T, f *Fleet) {
	t.Helper()
	for i := 0; i < 60; i++ {
		as, err := f.Refresh()
		if err != nil {
			t.Fatal(err)
		}
		if len(as) == 1 && as[0].Status == "stuck" {
			for j := 0; j < 5; j++ {
				f.Refresh()
				time.Sleep(20 * time.Millisecond)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("a1 never went stuck")
}

func stuckReports(t *testing.T, f *Fleet) []mail.Msg {
	t.Helper()
	var out []mail.Msg
	for _, m := range f.Mail().Peek(mail.Admiral) {
		if m.Kind == mail.KindReport && strings.Contains(m.Text, "stuck:") {
			out = append(out, m)
		}
	}
	return out
}

// A frozen working pane goes stuck, the admiral is told exactly once however
// many refreshes the episode spans, and nothing is paused or killed.
func TestRefreshStuckReportsOnceAndDoesNotPause(t *testing.T) {
	f, keyed := stuckFleet(t, "stuck_after = \"300ms\"\n")
	refreshUntilStuck(t, f)
	// Wait for the reporting goroutines, then refresh again: still one.
	time.Sleep(300 * time.Millisecond)
	f.Refresh()
	time.Sleep(100 * time.Millisecond)

	rs := stuckReports(t, f)
	if len(rs) != 1 {
		t.Fatalf("got %d stuck reports, want 1", len(rs))
	}
	if !strings.Contains(rs[0].Text, "no screen change") {
		t.Errorf("report text: %q", rs[0].Text)
	}
	if len(*keyed) != 0 {
		t.Errorf("stuck must not interrupt by default, keys sent: %v", *keyed)
	}
	a := f.Store.Load().Agents["a1"]
	if !a.Stuck.Stuck() || !a.Stuck.Notified {
		t.Fatalf("board stuck state: %+v", a.Stuck)
	}
	// Not paused, not killed: the pane is still there and still the agent's.
	if ps, _ := f.T.ListPanes(""); len(ps) == 0 {
		t.Error("the agent's pane is gone")
	}
	// The wake line names the agent and says it was not paused.
	b, _ := os.ReadFile(f.wakePath(mail.Supervisor))
	if l := string(b); !strings.Contains(l, "a1 looks stuck") || !strings.Contains(l, "Not paused") {
		t.Errorf("wake line: %q", l)
	}
}

// stuck_action = "interrupt" is the opt-in: Escape, once, and still no kill.
func TestRefreshStuckInterruptOptIn(t *testing.T) {
	f, keyed := stuckFleet(t, "stuck_after = \"300ms\"\nstuck_action = \"interrupt\"\n")
	refreshUntilStuck(t, f)
	time.Sleep(300 * time.Millisecond)
	f.Refresh()
	time.Sleep(100 * time.Millisecond)

	if got := *keyed; len(got) != 1 || got[0] != "Escape" {
		t.Fatalf("keys sent: %v, want one Escape", got)
	}
	if rs := stuckReports(t, f); len(rs) != 1 || !strings.Contains(rs[0].Text, "Escape sent") {
		t.Fatalf("reports: %+v", rs)
	}
}

// A one-shot `fleet ls` neither wakes the admiral nor burns the episode's
// one notification, even though it drains the queue (ADR-44): only the
// overview reports.
func TestRefreshStuckOnlyReportsWhenDraining(t *testing.T) {
	f, _ := stuckFleet(t, "stuck_after = \"300ms\"\n")
	f.DrainQueue, f.Housekeeping = true, false
	for i := 0; i < 40; i++ {
		as, _ := f.Refresh()
		if len(as) == 1 && as[0].Status == "stuck" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if a := f.Store.Load().Agents["a1"]; !a.Stuck.Stuck() {
		t.Fatal("a1 never went stuck")
	} else if a.Stuck.Notified {
		t.Error("a non-draining refresh claimed the notification")
	}
	if rs := stuckReports(t, f); len(rs) != 0 {
		t.Errorf("got %d reports from a non-draining refresh", len(rs))
	}
}

func TestStuckLine(t *testing.T) {
	got := StuckLine("a1", "the same error twice in a row: ✖ Error: build failed.", "none")
	if !strings.Contains(got, "a1 looks stuck: the same error twice in a row: ✖ Error: build failed.") {
		t.Errorf("got %q", got)
	}
	if !strings.Contains(got, "Not paused") || !strings.Contains(got, "fleet_reports") {
		t.Errorf("got %q", got)
	}
	if got := StuckLine("a1", "x", "interrupt"); !strings.Contains(got, "Escape sent") {
		t.Errorf("got %q", got)
	}
	long := StuckLine("a1", strings.Repeat("y", 200), "none")
	if !strings.Contains(long, "…") || len([]rune(long)) > 200 {
		t.Errorf("not capped: %q", long)
	}
}
