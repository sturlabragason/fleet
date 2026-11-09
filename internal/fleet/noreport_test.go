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
				return p.NoReport
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
