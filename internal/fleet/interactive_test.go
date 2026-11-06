package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/prefs"
)

// autoCloseFleet is mailFleet with auto_close_done on and agent a's dir a
// clean, merged repo, so only the interactive rules can keep it open.
func autoCloseFleet(t *testing.T) (*Fleet, *[]string) {
	t.Helper()
	f, pasted := mailFleet(t)
	fakeAdm(f, pasted, ptr(true))
	var closed []string
	f.CloseLater = func(n string) error { closed = append(closed, n); return nil }
	clean := repo(t)
	f.Store.Update(func(b *board.Board) error { b.Agents["a"].Dir = clean; return nil })
	cfg := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfg, []byte("[defaults]\nauto_close_done = true\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	pr, err := prefs.Load()
	if err != nil {
		t.Fatal(err)
	}
	f.Prefs = pr
	return f, &closed
}

// userIn makes the fleet see one attached client with pane active and its
// last key press at at.
func userIn(f *Fleet, pane string, at time.Time) {
	f.Clients = func() []ClientSeen { return []ClientSeen{{Pane: pane, Active: at}} }
}

// fleet#1: an agent the user talks to is never auto-closed on done.
func TestInteractiveAgentIsNotAutoClosed(t *testing.T) {
	f, closed := autoCloseFleet(t)
	f.Store.Update(func(b *board.Board) error { b.Agents["a"].Interactive = true; return nil })
	note, err := f.Report("a", "done", "replied")
	if err != nil {
		t.Fatal(err)
	}
	if note != "not auto-closed: interactive" || len(*closed) != 0 {
		t.Fatalf("note %q closed %v", note, *closed)
	}
}

// A pane the user has open, or typed in a minute ago, is not closed under
// them either, flag or no flag.
func TestPaneWithUserIsNotAutoClosed(t *testing.T) {
	f, closed := autoCloseFleet(t)
	pane := f.paneOf("a")
	userIn(f, pane, time.Now().Add(-time.Hour)) // focused, long idle
	if note, _ := f.Report("a", "done", "x"); !strings.HasPrefix(note, "not auto-closed: interactive") || len(*closed) != 0 {
		t.Fatalf("focused: note %q closed %v", note, *closed)
	}
	f.Mail().Inbox(mail.Supervisor, false, 0)
	userIn(f, "%999", time.Now()) // the user moved on to another pane...
	f.Store.Update(func(b *board.Board) error { b.Agents["a"].UserAt = time.Now().Add(-time.Minute); return nil })
	if note, _ := f.Report("a", "done", "y"); !strings.HasPrefix(note, "not auto-closed: interactive") || len(*closed) != 0 {
		t.Fatalf("typed a minute ago: note %q closed %v", note, *closed)
	}
	f.Mail().Inbox(mail.Supervisor, false, 0)
	// ...and long enough ago that the pane is fleet's again.
	f.Store.Update(func(b *board.Board) error {
		b.Agents["a"].UserAt = time.Now().Add(-board.UserHold - time.Minute)
		return nil
	})
	if note, _ := f.Report("a", "done", "z"); note != "auto-closing" || len(*closed) != 1 {
		t.Fatalf("user gone: note %q closed %v", note, *closed)
	}
}

// Refresh notes the user's key presses in an agent's pane on the board,
// so the agent counts as theirs after they move to another pane.
func TestRefreshRecordsUserKeys(t *testing.T) {
	f, _ := mailFleet(t)
	f.Store.Update(func(b *board.Board) error {
		for _, a := range b.Agents {
			a.Created = time.Now().Add(-time.Hour)
		}
		return nil
	})
	at := time.Now().Add(-2 * time.Second).Truncate(time.Second)
	userIn(f, f.paneOf("a"), at)
	if _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	as := f.Store.Load().Agents
	if !as["a"].UserAt.Equal(at) || !as["a"].WithUser(time.Now()) {
		t.Fatalf("a: user_at %v", as["a"].UserAt)
	}
	if !as["b"].UserAt.IsZero() || as["b"].WithUser(time.Now()) {
		t.Fatalf("b: user_at %v", as["b"].UserAt)
	}
}

// fleet#1: fleet_send must not paste into what the user is typing.
func TestSendBlocker(t *testing.T) {
	f, _ := mailFleet(t)
	pane := f.paneOf("a")
	if r := f.SendBlocker("a", true); r != "" {
		t.Fatalf("no user: %q", r)
	}
	userIn(f, pane, time.Now().Add(-5*time.Second))
	if r := f.SendBlocker("a", true); !strings.Contains(r, "the user typed in a's pane") {
		t.Fatalf("recent keys: %q", r)
	}
	if r := f.SendBlocker("a", false); r == "" {
		t.Fatal("keys alone (Enter) would submit the user's text too")
	}
	userIn(f, pane, time.Now().Add(-time.Hour))
	if r := f.SendBlocker("a", true); r != "" {
		t.Fatalf("focused but idle, not interactive: %q", r)
	}
	f.Store.Update(func(b *board.Board) error { b.Agents["a"].Interactive = true; return nil })
	if r := f.SendBlocker("a", true); !strings.Contains(r, "interactive") {
		t.Fatalf("interactive and open: %q", r)
	}
	userIn(f, "%999", time.Now())
	if r := f.SendBlocker("a", true); r != "" {
		t.Fatalf("interactive but the user is elsewhere: %q", r)
	}
}

// Text the user left in the input box is never merged into.
func TestSendBlockerUnsubmittedInput(t *testing.T) {
	f, _ := mailFleet(t)
	f.Clients = func() []ClientSeen { return nil }
	pane, err := f.T.Spawn(Session, WindowPrefix+"u1", f.Home, nil, "sh -c 'printf \"> half a thought\"; cat'")
	if err != nil {
		t.Fatal(err)
	}
	f.T.SetPaneOption(pane, "@fleet_agent", "u1")
	f.Store.Update(func(b *board.Board) error {
		b.Agents["u1"] = &board.Agent{Name: "u1", Profile: "cat", Patterns: "fake", PaneID: pane}
		return nil
	})
	var r string
	for i := 0; i < 50 && r == ""; i++ {
		time.Sleep(50 * time.Millisecond)
		r = f.SendBlocker("u1", true)
	}
	if !strings.Contains(r, "unsubmitted") || !strings.Contains(r, "half a thought") {
		t.Fatalf("got %q", r)
	}
}

// fleet#1: an interactive spawn is flagged on the board and told to report
// progress, not done, until the user ends the session.
func TestInteractiveSpawn(t *testing.T) {
	f, out := argvFleet(t, "")
	a, err := f.SpawnWith(SpawnOpts{Role: Role{Interactive: true}}, "ia1", "pa", t.TempDir(), "help me draft a reply")
	if err != nil {
		t.Fatal(err)
	}
	if !a.Interactive || !f.Store.Load().Agents["ia1"].Interactive {
		t.Fatal("not flagged interactive on the board")
	}
	got := readArg(t, out)
	if !strings.Contains(got, "talks to you directly") || !strings.Contains(got, "`progress`") {
		t.Fatalf("no interactive orientation:\n%.600q", got)
	}
}

// A key press from before the agent existed is not the user typing in it.
func TestNoteUserIgnoresKeysBeforeSpawn(t *testing.T) {
	now := time.Now()
	a := &board.Agent{Created: now}
	noteUser(a, []ClientSeen{{Pane: "%1", Active: now.Add(-time.Second)}}, "%1")
	if !a.UserAt.IsZero() {
		t.Fatalf("user_at %v from a key press before spawn", a.UserAt)
	}
	noteUser(a, []ClientSeen{{Pane: "%1", Active: now.Add(time.Second)}}, "%1")
	if a.UserAt.IsZero() {
		t.Fatal("key press after spawn not recorded")
	}
}
