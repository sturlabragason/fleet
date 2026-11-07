package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/supervisor"
)

func twoAdmirals(t *testing.T) *Fleet {
	t.Helper()
	f := squadFleet(t)
	if _, err := supervisor.Add(f.Home, "print", "", 3); err != nil {
		t.Fatal(err)
	}
	return f
}

func spawnFor(t *testing.T, f *Fleet, adm, name string) {
	t.Helper()
	if _, err := f.SpawnWith(SpawnOpts{Role: Role{Admiral: adm}}, name, "cat", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
}

// A report lands in its owner's box only; an agent with no admiral is main's.
func TestAdmiralReportRouting(t *testing.T) {
	f := twoAdmirals(t)
	spawnFor(t, f, "print", "p1")
	spawnFor(t, f, "", "m1")
	if f.OwnerOf("m1") != board.Main || f.OwnerOf("p1") != "print" {
		t.Fatalf("owners: %q %q", f.OwnerOf("m1"), f.OwnerOf("p1"))
	}
	if _, err := f.Report("p1", "done", "printed"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Report("m1", "done", "mained"); err != nil {
		t.Fatal(err)
	}
	if ms := f.Mail().Peek("admiral.print"); len(ms) != 1 || ms[0].From != "p1" {
		t.Fatalf("print box: %+v", ms)
	}
	if ms := f.Mail().Peek(mail.Admiral); len(ms) != 1 || ms[0].From != "m1" {
		t.Fatalf("main box: %+v", ms)
	}
	if u := f.UnreadBy(); u["print"] != 1 || u["main"] != 1 {
		t.Fatalf("unread by: %v", u)
	}
}

func TestAdmiralScope(t *testing.T) {
	f := twoAdmirals(t)
	spawnFor(t, f, "print", "p1")
	if err := f.CheckScope("", "p1"); err == nil {
		t.Fatal("main acted on print's agent")
	}
	if err := f.CheckScope("print", "p1"); err != nil {
		t.Fatal(err)
	}
}

func TestAdmiralGiveMovesReports(t *testing.T) {
	f := twoAdmirals(t)
	spawnFor(t, f, "", "m1")
	f.Report("m1", "done", "one")
	n, err := f.Give("", "m1", "print")
	if err != nil || n != 1 {
		t.Fatalf("give: %d %v", n, err)
	}
	if f.OwnerOf("m1") != "print" {
		t.Fatal("owner not moved")
	}
	if len(f.Mail().Peek(mail.Admiral)) != 0 || len(f.Mail().Peek("admiral.print")) != 1 {
		t.Fatal("report did not move with the agent")
	}
	if _, err := f.Give("main", "m1", "print"); err == nil {
		t.Fatal("main gave away an agent it no longer owns")
	}
}

func TestAdmiralCloseReturnsToMain(t *testing.T) {
	f := twoAdmirals(t)
	spawnFor(t, f, "print", "p1")
	f.Report("p1", "done", "x")
	a, r, err := f.CloseAdmiral("print")
	if err != nil || a != 1 || r != 1 {
		t.Fatalf("close: %d %d %v", a, r, err)
	}
	if f.OwnerOf("p1") != board.Main || len(f.Mail().Peek(mail.Admiral)) != 1 {
		t.Fatal("agent or report not back with main")
	}
	if supervisor.Exists(f.Home, "print") {
		t.Fatal("still registered")
	}
	if _, _, err := f.CloseAdmiral("main"); err == nil {
		t.Fatal("main closed")
	}
}

// Under team policy admirals need a grant; under open they do not.
func TestAdmiralMailPolicy(t *testing.T) {
	f := twoAdmirals(t)
	if _, err := f.MailSend(mail.Admiral, "admiral.print", "hi"); err == nil {
		t.Fatalf("delivered without a grant (policy %s)", f.MailPolicy())
	}
	if err := f.AccessDecide(mail.Admiral, "admiral.print", "grant"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.MailSend(mail.Admiral, "admiral.print", "hi"); err != nil {
		t.Fatalf("after grant: %v", err)
	}
	if ms := f.Mail().PeekMsgs("admiral.print"); len(ms) != 2 || ms[1].Text != "hi" {
		t.Fatalf("print inbox: %+v", ms)
	}
}

// Admirals reach each other without a grant under agent_mail = open.
func TestAdmiralMailOpenPolicy(t *testing.T) {
	f := twoAdmirals(t)
	cfg := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfg, []byte("[defaults]\nagent_mail = \"open\"\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	if _, err := f.MailSend("admiral.print", mail.Admiral, "open hi"); err != nil {
		t.Fatalf("open policy refused: %v", err)
	}
	if ms := f.Mail().PeekMsgs(mail.Admiral); len(ms) != 1 || ms[0].Text != "open hi" {
		t.Fatalf("main inbox: %+v", ms)
	}
	if _, err := f.MailSend(mail.Admiral, "*", "all"); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if ms := f.Mail().PeekMsgs("admiral.print"); len(ms) != 1 {
		t.Fatalf("print inbox: %+v", ms)
	}
}

// A report from print's agent is typed into print's pane, not main's.
func TestWakeLandsInOwningAdmiralPane(t *testing.T) {
	f := twoAdmirals(t)
	spawnFor(t, f, "print", "p1")
	idle := ptr(true)
	var typed []string
	f.SupPane = func() AdmiralView { return AdmiralView{Pane: "%main", Idle: *idle, Box: true} }
	f.AdmiralPane = func(name string) AdmiralView {
		if name != "print" {
			t.Errorf("pane lookup for %q", name)
		}
		return AdmiralView{Pane: "%print", Idle: *idle, Box: true}
	}
	f.TypeText = func(pane, text string) error { typed = append(typed, pane+" "+text); return nil }
	f.PressKeys = func(pane string, keys ...string) error { return nil }
	if _, err := f.Report("p1", "blocked", "needs creds"); err != nil {
		t.Fatal(err)
	}
	f.DeliverWake()
	if len(typed) == 0 {
		t.Fatal("nothing typed")
	}
	for _, ty := range typed { // the fake never submits, so a retry may type again
		if !strings.HasPrefix(ty, "%print ") || !strings.Contains(ty, "p1") {
			t.Fatalf("typed %q", typed)
		}
	}
}

// Closing an admiral with a squad hands the leader and its member back to main.
func TestAdmiralCloseWithSquad(t *testing.T) {
	f := twoAdmirals(t)
	if _, err := f.SpawnWith(SpawnOpts{Role: Role{Leader: true, Admiral: "print"}}, "lead", "cat", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	spawnMember(t, f, "lead", "m1")
	if f.OwnerOf("lead") != "print" || f.OwnerOf("m1") != "print" {
		t.Fatalf("owners before: %q %q", f.OwnerOf("lead"), f.OwnerOf("m1"))
	}
	f.Report("m1", "done", "squad work")
	a, _, err := f.CloseAdmiral("print")
	if err != nil || a != 2 {
		t.Fatalf("close: %d %v", a, err)
	}
	if f.OwnerOf("lead") != board.Main || f.OwnerOf("m1") != board.Main {
		t.Fatalf("owners after: %q %q", f.OwnerOf("lead"), f.OwnerOf("m1"))
	}
	// The squad stays together: the member still reports to its leader.
	if len(f.Mail().Peek("lead")) != 1 {
		t.Fatalf("leader inbox: %+v", f.Mail().Peek("lead"))
	}
}
