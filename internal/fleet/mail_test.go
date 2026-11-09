package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/tmux"
)

func mailFleet(t *testing.T) (*Fleet, *[]string) {
	t.Helper()
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte("[cat]\ncommand = \"sh\"\nargs = [\"-c\", \"cat\"]\npatterns = \"fake\"\n"), 0o644)
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	var pasted []string
	f.Paste = func(pane, text string) error { pasted = append(pasted, pane+" "+text); return nil }
	for _, n := range []string{"a", "b"} {
		if _, err := f.Spawn(n, "cat", home, ""); err != nil {
			t.Fatal(err)
		}
	}
	return f, &pasted
}

func setStatus(t *testing.T, f *Fleet, name, st string) {
	t.Helper()
	f.Store.Update(func(b *board.Board) error { b.Agents[name].Status = st; return nil })
}

func TestMailNudgeIdleOncePerBatch(t *testing.T) {
	f, pasted := mailFleet(t)
	setStatus(t, f, "b", "working")
	if _, err := f.MailRequest("a", "b", "api"); err != nil {
		t.Fatal(err)
	}
	if len(*pasted) != 0 {
		t.Fatalf("nudged a working agent: %v", *pasted)
	}
	if err := f.Mail().Respond("b", "a", true); err != nil {
		t.Fatal(err)
	}
	f.Mail().Inbox("b", false, 0)
	setStatus(t, f, "b", "idle")
	if _, err := f.MailSend("a", "b", "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.MailSend("a", "b", "two"); err != nil {
		t.Fatal(err)
	}
	pb, _ := f.Pane("b")
	if len(*pasted) != 1 || (*pasted)[0] != pb+" "+NudgeLine(1, "a") {
		t.Fatalf("pasted %q", *pasted)
	}
	f.Mail().Inbox("b", false, 0)
	f.MailSend("a", "b", "three")
	if len(*pasted) != 2 {
		t.Fatalf("new batch not nudged: %q", *pasted)
	}
	setStatus(t, f, "b", "waiting")
	f.Mail().Inbox("b", false, 0)
	f.MailSend("a", "b", "four")
	if len(*pasted) != 2 {
		t.Fatalf("nudged a waiting agent: %q", *pasted)
	}
	if _, err := f.MailSend("a", "ghost", "x"); err == nil || err.Error() != `no agent "ghost"` {
		t.Fatalf("unknown recipient: %v", err)
	}
}

func TestCloseRevokesGrants(t *testing.T) {
	f, _ := mailFleet(t)
	if err := f.AccessDecide("a", "b", "grant"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close("a"); err != nil {
		t.Fatal(err)
	}
	if len(f.Mail().All()) != 0 {
		t.Fatalf("grants left: %+v", f.Mail().All())
	}
	if _, err := f.Mail().Send("b", "a", "x"); err == nil {
		t.Fatal("send to closed agent")
	}
}

func TestWhoami(t *testing.T) {
	f, _ := mailFleet(t)
	pa, _ := f.Pane("a")
	t.Setenv("TMUX_PANE", pa)
	t.Setenv("TMUX", "/tmp/elsewhere,1,0")
	if me := f.Whoami(""); me != "" {
		t.Fatalf("pane of another tmux server resolved to %q", me)
	}
	t.Setenv("TMUX", f.T.SocketPath()+",1,0")
	if me := f.Whoami(""); me != "a" {
		t.Fatalf("whoami %q", me)
	}
	if me := f.Whoami("b"); me != "b" {
		t.Fatalf("override %q", me)
	}
	if _, err := f.Me("b"); err == nil {
		t.Fatal("agent a acted as b with --as")
	}
	if err := f.UserOnly(); err == nil {
		t.Fatal("fleet access decisions allowed from an agent pane")
	}
	t.Setenv("TMUX_PANE", "")
	if err := f.UserOnly(); err != nil {
		t.Fatalf("user outside a pane refused: %v", err)
	}
	if _, err := f.Me(""); err != ErrNotAgent {
		t.Fatalf("me: %v", err)
	}
}

func TestUserIsReservedName(t *testing.T) {
	if validName("user") || !validName("user2") {
		t.Fatal(`"user" must be reserved, other names not`)
	}
}

func TestAdoptNameNeverUser(t *testing.T) {
	f, _ := mailFleet(t)
	if n, err := f.adoptName("", "/home/user"); err != nil || n != "adopted" {
		t.Fatalf("adopt name from /home/user: %q %v", n, err)
	}
}

func TestMailPolicy(t *testing.T) {
	f, _ := mailFleet(t) // a and b: no team, dir not in a git repo
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	for _, n := range []string{"c", "d"} {
		if _, err := f.Spawn(n, "cat", repo, ""); err != nil {
			t.Fatal(err)
		}
	}
	access := func(me, other string) string {
		for _, p := range f.MailAgents(me) {
			if p.Name == other {
				return p.Access
			}
		}
		return "?"
	}
	// Default "team": no team and no shared repo asks; same repo is free.
	if _, err := f.MailSend("a", "b", "x"); err == nil {
		t.Fatal("a->b without team or repo")
	}
	if _, err := f.MailSend("c", "d", "same repo"); err != nil || access("c", "d") != "free (repo)" {
		t.Fatalf("same repo: %v %q", err, access("c", "d"))
	}
	// A team overrides the repo: different teams (or one teamless) ask.
	f.Teams.Create("acme", "")
	f.Teams.Add("acme", "a", "c")
	if _, err := f.MailSend("c", "d", "x"); err == nil || access("c", "d") != "none" {
		t.Fatalf("team member to teamless agent in the same repo: %v %q", err, access("c", "d"))
	}
	if _, err := f.MailSend("a", "c", "teammate"); err != nil || access("c", "a") != "free (team)" {
		t.Fatalf("teammates: %v %q", err, access("c", "a"))
	}
	if r, err := f.MailSend("c", "*", "all"); err != nil || len(r) != 1 || r[0] != "a" {
		t.Fatalf("broadcast: %v %v", r, err)
	}
	// A squad leader on the same repo is reachable from outside its team,
	// and a refusal to one of its members names it (fleet#35).
	if _, err := f.Spawn("e", "cat", repo, ""); err != nil {
		t.Fatal(err)
	}
	f.Store.Update(func(b *board.Board) error {
		b.Agents["d"].Leader, b.Agents["e"].LeadBy = true, "d"
		return nil
	})
	if _, err := f.MailSend("c", "d", "hand-off"); err != nil || access("c", "d") != "free (lead)" || access("d", "c") != "free (lead)" {
		t.Fatalf("to a lead: %v %q %q", err, access("c", "d"), access("d", "c"))
	}
	want := "no access to e: request it with fleet_access_request (to e), or message d (lead of e's team), who you may reach freely"
	if _, err := f.MailSend("c", "e", "x"); err == nil || err.Error() != want {
		t.Fatalf("to a lead's member: %v", err)
	}
	if _, err := f.MailSend("a", "b", "x"); err == nil || err.Error() != "no access to b: request it with fleet_access_request" {
		t.Fatalf("no lead to name: %v", err)
	}
	// "open" allows everything; "ask" is the old request-first flow.
	os.WriteFile(f.Prefs.Path, []byte("[defaults]\nagent_mail = \"open\"\n"), 0o644)
	f.Prefs, _ = prefs.Load()
	if _, err := f.MailSend("b", "d", "open"); err != nil || access("b", "d") != "free (open)" {
		t.Fatalf("open: %v %q", err, access("b", "d"))
	}
	os.WriteFile(f.Prefs.Path, []byte("[defaults]\nagent_mail = \"ask\"\n"), 0o644)
	f.Prefs, _ = prefs.Load()
	if _, err := f.MailSend("a", "c", "x"); err == nil {
		t.Fatal("ask policy let teammates through")
	}
}
