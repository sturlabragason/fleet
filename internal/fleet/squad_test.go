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

// squadFleet is a fleet with the cat profile and no config file, so the
// user's own config.toml can never change a result.
func squadFleet(t *testing.T) *Fleet {
	t.Helper()
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte("[cat]\ncommand = \"sh\"\nargs = [\"-c\", \"cat\"]\npatterns = \"fake\"\n"+
		// box shows an input box on screen, so a wake may be typed into it.
		"[box]\ncommand = \"sh\"\nargs = [\"-c\", \"printf '> '; cat\"]\npatterns = \"fake\"\n"), 0o644)
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	f.Paste = func(pane, text string) error { return nil }
	return f
}

func spawnLeader(t *testing.T, f *Fleet, name string) *board.Agent {
	t.Helper()
	a, err := f.SpawnWith(SpawnOpts{Role: Role{Leader: true}}, name, "cat", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func spawnMember(t *testing.T, f *Fleet, leader, name string) *board.Agent {
	t.Helper()
	a, err := f.SquadSpawn(leader, SpawnOpts{}, name, "cat", t.TempDir(), "")
	if err != nil {
		t.Fatalf("squad spawn %s: %v", name, err)
	}
	return a
}

// A squad member's report goes to its leader's mailbox and wakes the
// leader's own pane; the admiral sees nothing of it. The leader's own
// report still goes to the admiral.
func TestSquadReportsGoToTheLeader(t *testing.T) {
	f := squadFleet(t)
	spawnLeader(t, f, "lead")
	spawnMember(t, f, "lead", "m1")

	if _, err := f.Report("m1", "done", "member finished"); err != nil {
		t.Fatal(err)
	}
	if ms := f.Mail().Peek(mail.Admiral); len(ms) != 0 {
		t.Fatalf("admiral got the member's report: %+v", ms)
	}
	ms := f.Mail().Peek("lead")
	if len(ms) != 1 || ms[0].From != "m1" || ms[0].Kind != mail.KindReport {
		t.Fatalf("leader inbox: %+v", ms)
	}
	// The wake queue is the leader's own, in the same four-field format.
	b, err := os.ReadFile(f.wakePath("lead"))
	if err != nil {
		t.Fatalf("no leader wake queue: %v", err)
	}
	line := strings.TrimRight(string(b), "\n")
	if n := len(strings.Split(line, "\t")); n != 4 {
		t.Fatalf("wake queue line has %d fields, want 4: %q", n, line)
	}
	if _, err := os.Stat(f.wakePath(mail.Admiral)); !os.IsNotExist(err) {
		t.Fatalf("admiral wake queue written for a squad report: %v", err)
	}

	// The leader reports upward as any agent does.
	if _, err := f.Report("lead", "done", "squad finished"); err != nil {
		t.Fatal(err)
	}
	if ms := f.Mail().Peek(mail.Admiral); len(ms) != 1 || ms[0].From != "lead" {
		t.Fatalf("admiral inbox: %+v", ms)
	}
}

// The leader's pane is woken the same way the admiral's is: one line typed
// into an idle, empty input box.
func TestSquadReportWakesTheLeaderPane(t *testing.T) {
	f := squadFleet(t)
	if _, err := f.SpawnWith(SpawnOpts{Role: Role{Leader: true}}, "lead", "box", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	spawnMember(t, f, "lead", "m1")

	// The wake is typed into the leader's own pane; Enter is faked, so the
	// line is read back off the screen instead of being submitted.
	var typed []string
	pane := f.Store.Load().Agents["lead"].PaneID
	f.SupPane = func() AdmiralView { return AdmiralView{} } // the admiral has no pane here
	f.PressKeys = func(p string, keys ...string) error {
		for _, k := range keys {
			if k == "Enter" && p == pane {
				typed = append(typed, f.boxView("lead").Input)
			}
		}
		return nil
	}
	if _, err := f.Report("m1", "blocked", "needs a decision"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50 && len(typed) == 0; i++ {
		f.DeliverWake()
		time.Sleep(100 * time.Millisecond)
	}
	// The line names fleet_squad_reports: a leader has no fleet_reports.
	if want := ReportLineTo("lead", "m1", "blocked", "needs a decision"); !strings.Contains(want, "fleet_squad_reports") {
		t.Fatalf("leader wake line: %q", want)
	} else if len(typed) != 1 || typed[0] != want {
		t.Fatalf("typed %q, want %q", typed, want)
	}
}

// A leader may only act on its own agents.
func TestSquadScope(t *testing.T) {
	f := squadFleet(t)
	spawnLeader(t, f, "lead")
	spawnMember(t, f, "lead", "m1")
	if _, err := f.Spawn("outsider", "cat", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.SquadMember("lead", "m1"); err != nil {
		t.Fatalf("own member refused: %v", err)
	}
	for _, n := range []string{"outsider", "nobody", ""} {
		if _, err := f.SquadMember("lead", n); err == nil {
			t.Fatalf("leader allowed to act on %q", n)
		}
	}
	// And a leader cannot adopt someone else's agent by spawning over it.
	if _, err := f.SquadSpawn("outsider", SpawnOpts{}, "m2", "cat", t.TempDir(), ""); err == nil {
		t.Fatal("a non-leader spawned a squad agent")
	}
}

// max_members caps a squad and max_depth stops a member leading one.
func TestSquadCaps(t *testing.T) {
	f := squadFleet(t)
	cfg := filepath.Join(f.Home, "config.toml")
	os.WriteFile(cfg, []byte("[squad]\nmax_members = 2\nmax_depth = 1\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)

	spawnLeader(t, f, "lead")
	spawnMember(t, f, "lead", "m1")
	spawnMember(t, f, "lead", "m2")
	if _, err := f.SquadSpawn("lead", SpawnOpts{}, "m3", "cat", t.TempDir(), ""); err == nil {
		t.Fatal("max_members not enforced")
	} else if !strings.Contains(err.Error(), "max_members") {
		t.Fatalf("unhelpful cap error: %v", err)
	}
	// Depth 1: a member may not be a leader of its own.
	_, err := f.SpawnWith(SpawnOpts{Role: Role{Leader: true, LeadBy: "lead"}}, "m4", "cat", t.TempDir(), "")
	if err == nil || !strings.Contains(err.Error(), "max_depth") {
		t.Fatalf("max_depth not enforced: %v", err)
	}
	// Closing one frees a slot.
	if err := f.Close("m2"); err != nil {
		t.Fatal(err)
	}
	spawnMember(t, f, "lead", "m3")
}

// Closing a leader warns about its squad and hands the members back to the
// admiral; closing with the squad takes them too.
func TestSquadClose(t *testing.T) {
	f := squadFleet(t)
	spawnLeader(t, f, "lead")
	spawnMember(t, f, "lead", "m1")
	spawnMember(t, f, "lead", "m2")

	note := SquadNote("lead", f.Squad("lead"))
	if !strings.Contains(note, "m1") || !strings.Contains(note, "m2") {
		t.Fatalf("close note: %q", note)
	}
	if err := f.Close("lead"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"m1", "m2"} {
		a, ok := f.Store.Load().Agents[n]
		if !ok {
			t.Fatalf("%s was closed with its leader", n)
		}
		if a.LeadBy != "" {
			t.Fatalf("%s still reports to a leader that is gone: %q", n, a.LeadBy)
		}
	}
	// The mirror team goes with the leader, so the ex-members are not left
	// sharing a colour and free mail under a dead leader's name.
	if ms, err := f.Teams.Members(SquadTeam("lead")); err == nil && len(ms) > 0 {
		t.Fatalf("squad team outlived its leader: %v", ms)
	}
	if f.SameSquad("m1", "m2") {
		t.Error("detached members still count as one squad")
	}
	// Nothing is left queued to type into whatever agent gets this name next.
	if _, err := os.Stat(f.wakePath("lead")); !os.IsNotExist(err) {
		t.Errorf("leader wake queue survived the close: %v", err)
	}

	// Their reports now reach the admiral, not a dead mailbox.
	if _, err := f.Report("m1", "done", "orphan"); err != nil {
		t.Fatal(err)
	}
	if ms := f.Mail().Peek(mail.Admiral); len(ms) != 1 || ms[0].From != "m1" {
		t.Fatalf("admiral inbox: %+v", ms)
	}

	// close --squad / squad=true takes the members with the leader.
	spawnLeader(t, f, "lead2")
	spawnMember(t, f, "lead2", "n1")
	spawnMember(t, f, "lead2", "n2")
	if rs := f.SquadCloseAll("lead2"); len(rs) != 2 {
		t.Fatalf("close all: %+v", rs)
	}
	if err := f.Close("lead2"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"lead2", "n1", "n2"} {
		if _, ok := f.Store.Load().Agents[n]; ok {
			t.Fatalf("%s left on the board", n)
		}
	}
}

// A squad is a team: leader and members message each other without a grant
// under the default agent_mail policy, and an outsider still has to ask.
func TestSquadMailIsFree(t *testing.T) {
	f := squadFleet(t)
	spawnLeader(t, f, "lead")
	spawnMember(t, f, "lead", "m1")
	spawnMember(t, f, "lead", "m2")
	if _, err := f.Spawn("outsider", "cat", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	if !f.SameSquad("lead", "m1") || !f.SameSquad("m1", "m2") {
		t.Fatal("squad members cannot reach each other")
	}
	if f.SameSquad("lead", "outsider") || f.SameSquad("m1", "outsider") {
		t.Fatal("an outsider counts as squad")
	}
	policy := f.MailPolicy()
	if r := f.mailReason(policy, "lead", "m1", nil); r == "" {
		t.Fatal("leader and member must message without a grant")
	}
	if r := f.mailReason(policy, "m1", "outsider", map[string]string{}); r != "" {
		t.Fatalf("outsider got free access: %q", r)
	}
}

// A question a member put to its leader must not die with the leader: the
// close re-files the squad's unread reports to the admiral (ADR-59).
func TestSquadCloseRefilesUnreadReports(t *testing.T) {
	f := squadFleet(t)
	f.SupPane = func() AdmiralView { return AdmiralView{} }
	spawnLeader(t, f, "lead")
	spawnMember(t, f, "lead", "m1")
	if _, err := f.Report("m1", "question", "which database?"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close("lead"); err != nil {
		t.Fatal(err)
	}
	ms := f.Mail().Peek(mail.Admiral)
	if len(ms) != 1 || ms[0].From != "m1" || ms[0].Kind != mail.KindReport ||
		!strings.HasPrefix(ms[0].Text, "question: which database?") {
		t.Fatalf("admiral inbox after the leader closed: %+v", ms)
	}
	b, err := os.ReadFile(f.wakePath(mail.Admiral))
	if err != nil || !strings.Contains(string(b), ms[0].ID+"\tm1\tquestion\t") {
		t.Fatalf("re-filed question not queued to wake the admiral: %q, %v", b, err)
	}
}

// A leader that happens to share a name with the user's own team must not
// join it, and closing it must not delete it (ADR-59).
func TestSquadCloseKeepsAUserTeamOfTheSameName(t *testing.T) {
	f := squadFleet(t)
	if _, err := f.Spawn("outsider", "cat", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Teams.Add("lead", "outsider"); err != nil {
		t.Fatal(err)
	}
	spawnLeader(t, f, "lead")
	spawnMember(t, f, "lead", "m1")
	if f.Teams.SameTeam("m1", "outsider") || f.Teams.SameTeam("lead", "outsider") {
		t.Error("the squad was mirrored into the user's team")
	}
	if err := f.Close("lead"); err != nil {
		t.Fatal(err)
	}
	ms, err := f.Teams.Members("lead")
	if err != nil || len(ms) != 1 || ms[0] != "outsider" {
		t.Fatalf("user's team after the leader closed: %v, %v", ms, err)
	}
}

// A peer nudge moves the inbox's nudge mark past a queued report; the
// report wake must still be typed (ADR-59).
func TestPeerNudgeDoesNotSuppressSquadWake(t *testing.T) {
	f := squadFleet(t)
	if _, err := f.SpawnWith(SpawnOpts{Role: Role{Leader: true}}, "lead", "box", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	spawnMember(t, f, "lead", "m1")
	var typed []string
	pane := f.Store.Load().Agents["lead"].PaneID
	f.SupPane = func() AdmiralView { return AdmiralView{} }
	f.PressKeys = func(p string, keys ...string) error {
		for _, k := range keys {
			if k == "Enter" && p == pane {
				typed = append(typed, f.boxView("lead").Input)
			}
		}
		return nil
	}
	m, err := f.Mail().ReportTo("m1", "lead", "blocked", "needs a decision")
	if err != nil {
		t.Fatal(err)
	}
	// What nudge() does when a peer message lands in the same inbox.
	if _, ok, err := f.Mail().ClaimNudge("lead"); !ok || err != nil {
		t.Fatalf("peer nudge claim: %v %v", ok, err)
	}
	line := ReportLineTo("lead", "m1", "blocked", "needs a decision")
	f.wakeBox("lead", m.ID, "m1", "blocked", line)
	for i := 0; i < 50 && len(typed) == 0; i++ {
		f.DeliverWake()
		time.Sleep(100 * time.Millisecond)
	}
	if len(typed) != 1 || typed[0] != line {
		t.Fatalf("typed %q, want the report wake %q", typed, line)
	}
}
