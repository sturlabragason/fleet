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
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// An agent handed a task whose turn ends with no done/blocked/question
// report is flagged on the board and the pane, and its owner is told once
// per task (ADR-68).
func TestRefreshNoReportFlagsAndTellsOncePerTask(t *testing.T) {
	old := board.NoReportAfter
	board.NoReportAfter = 0
	t.Cleanup(func() { board.NoReportAfter = old })

	home := t.TempDir()
	cfg := filepath.Join(home, "config.toml")
	os.WriteFile(cfg, []byte("[defaults]\nplaybook = false\nworktree = false\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[quiet]
command = "sh"
args = ["-c", "printf '> \n'; sleep 600"]
patterns = "fake"
`), 0o644)
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	f.DrainQueue, f.Housekeeping = true, true
	f.SupPane = func() AdmiralView { return AdmiralView{} } // never idle: nothing typed
	if _, err := f.Spawn("a1", "quiet", home, ""); err != nil {
		t.Fatal(err)
	}
	f.notePrompt("a1")

	tagged := func() bool {
		ps, _ := f.T.ListPanes("")
		for _, p := range ps {
			if p.Agent == "a1" {
				return p.NoReport == "1"
			}
		}
		return false
	}
	notes := func() int {
		time.Sleep(100 * time.Millisecond) // the note is sent off the Refresh goroutine
		n := 0
		for _, m := range f.Mail().Peek(mail.Supervisor) {
			if strings.Contains(m.Text, "no report:") {
				n++
			}
		}
		return n
	}
	refreshUntil := func(what string, ok func(*board.Agent) bool) {
		t.Helper()
		for i := 0; i < 50; i++ {
			f.Refresh()
			if ok(f.Store.Load().Agents["a1"]) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("a1 never %s: %+v", what, f.Store.Load().Agents["a1"])
	}

	refreshUntil("flagged", func(a *board.Agent) bool { return a.NoReport(time.Now()) })
	f.Refresh()
	if !tagged() {
		t.Error("pane not tagged @fleet_noreport")
	}
	if n := notes(); n != 1 {
		t.Fatalf("got %d no-report notes, want 1", n)
	}
	b, _ := os.ReadFile(f.wakePath(mail.Supervisor))
	if !strings.Contains(string(b), "a1 stopped mid-task without a report") {
		t.Errorf("wake line: %q", b)
	}

	// A real report ends it and clears the border mark.
	if _, err := f.Report("a1", "blocked", "need a token"); err != nil {
		t.Fatal(err)
	}
	refreshUntil("cleared", func(a *board.Agent) bool { return !a.NoReport(time.Now()) })
	f.Refresh()
	if tagged() {
		t.Error("@fleet_noreport left on the pane after a report")
	}

	// A new task re-arms the note.
	time.Sleep(10 * time.Millisecond)
	f.notePrompt("a1")
	refreshUntil("flagged again", func(a *board.Agent) bool { return a.NoReport(time.Now()) })
	f.Refresh()
	if n := notes(); n != 2 {
		t.Fatalf("got %d no-report notes after a second task, want 2", n)
	}
}

// An interactive agent whose turn ends with no report gets the soft mark on
// its pane, and nobody is woken: the user is the one talking to it (ADR-68
// amendment, fleet#8).
func TestRefreshUserNoReportMarksSoftlyAndWakesNobody(t *testing.T) {
	old := board.NoReportAfter
	board.NoReportAfter = 0
	t.Cleanup(func() { board.NoReportAfter = old })

	home := t.TempDir()
	cfg := filepath.Join(home, "config.toml")
	os.WriteFile(cfg, []byte("[defaults]\nplaybook = false\nworktree = false\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	// One turn: busy for a second, then back at the prompt.
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[turn]
command = "sh"
args = ["-c", "printf '● Task: logging everything now\n'; sleep 1; printf '\033[2J\033[H> \n'; sleep 600"]
patterns = "fake"
`), 0o644)
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	f.DrainQueue, f.Housekeeping = true, true
	f.SupPane = func() AdmiralView { return AdmiralView{} }
	if _, err := f.Spawn("a1", "turn", home, ""); err != nil {
		t.Fatal(err)
	}
	f.Store.Update(func(b *board.Board) error {
		b.Agents["a1"].Interactive = true
		return nil
	})
	f.notePrompt("a1") // would arm the hard flag, were it not the user's

	tag := func() string {
		ps, _ := f.T.ListPanes("")
		for _, p := range ps {
			if p.Agent == "a1" {
				return p.NoReport
			}
		}
		return ""
	}
	refreshUntil := func(what string, ok func(*board.Agent) bool) {
		t.Helper()
		for i := 0; i < 80; i++ {
			f.Refresh()
			if ok(f.Store.Load().Agents["a1"]) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("a1 never %s: %+v", what, f.Store.Load().Agents["a1"])
	}

	refreshUntil("worked", func(a *board.Agent) bool { return !a.TurnAt.IsZero() })
	refreshUntil("soft-flagged", func(a *board.Agent) bool { return a.UserNoReport(time.Now()) })
	f.Refresh()
	if a := f.Store.Load().Agents["a1"]; a.NoReport(time.Now()) {
		t.Error("the user's agent got the hard flag")
	}
	if got := tag(); got != "user" {
		t.Errorf("@fleet_noreport = %q, want user", got)
	}
	time.Sleep(100 * time.Millisecond)
	for _, m := range f.Mail().Peek(mail.Supervisor) {
		if strings.Contains(m.Text, "no report:") {
			t.Fatalf("the admiral was told about the user's agent: %q", m.Text)
		}
	}
	if _, err := os.Stat(f.wakePath(mail.Supervisor)); err == nil {
		t.Error("a wake line was queued for the user's agent")
	}

	// A progress report, the interactive ending, clears it.
	if _, err := f.Report("a1", "progress", "row 38 logged"); err != nil {
		t.Fatal(err)
	}
	refreshUntil("cleared", func(a *board.Agent) bool { return !a.UserNoReport(time.Now()) })
	f.Refresh()
	if got := tag(); got != "" {
		t.Errorf("@fleet_noreport = %q after a progress report", got)
	}
}

func TestNewTurn(t *testing.T) {
	now := time.Now()
	turn := now.Add(-time.Hour)
	for _, c := range []struct {
		name       string
		prev, next string
		since      time.Duration
		turnAt     time.Time
		want       bool
	}{
		{"first work", "unknown", "working", 0, time.Time{}, true},
		{"after starting", "starting", "working", 0, turn, true},
		{"after a long idle", "idle", "working", time.Minute, turn, true},
		{"after a short idle inside a turn", "idle", "working", time.Second, turn, false},
		{"a menu answered", "waiting", "working", time.Minute, turn, false},
		{"going idle", "working", "idle", time.Minute, turn, false},
	} {
		a := &board.Agent{TurnAt: c.turnAt, StatusSince: now.Add(-c.since)}
		if got := newTurn(a, c.prev, c.next, now); got != c.want {
			t.Errorf("%s: newTurn = %v, want %v", c.name, got, c.want)
		}
	}
}

// A pane the patterns read as idle while its screen keeps changing is still
// at work (agy editing files, fleet#12): no flag and no note.
func TestRefreshNoReportWaitsForAStillScreen(t *testing.T) {
	old := board.NoReportAfter
	board.NoReportAfter = 3 * time.Second // wide: a loaded machine can stall the loop
	t.Cleanup(func() { board.NoReportAfter = old })

	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "nonexistent.toml"))
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[busy]
command = "sh"
args = ["-c", "i=0; while :; do i=$((i+1)); printf '\033[H\033[2Jedited file %d\n> \n' $i; sleep 0.1; done"]
patterns = "fake"
`), 0o644)
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	f.DrainQueue, f.Housekeeping = true, true
	f.SupPane = func() AdmiralView { return AdmiralView{} }
	if _, err := f.Spawn("a1", "busy", home, ""); err != nil {
		t.Fatal(err)
	}
	f.notePrompt("a1")

	sawIdle := false
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		f.Refresh()
		a := f.Store.Load().Agents["a1"]
		sawIdle = sawIdle || a.Status == "idle"
		if a.NoReport(time.Now()) {
			t.Fatalf("flagged no report while the screen changes: %+v", a)
		}
	}
	if !sawIdle {
		t.Fatal("the patterns never read the pane as idle; the test proves nothing")
	}
	time.Sleep(100 * time.Millisecond)
	for _, m := range f.Mail().Peek(mail.Supervisor) {
		if strings.Contains(m.Text, "no report:") {
			t.Fatalf("owner told: %q", m.Text)
		}
	}
}
