package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/supervisor"
)

func TestReportWakesIdleSupervisorOncePerBatch(t *testing.T) {
	f, pasted := mailFleet(t)
	idle := false
	fakeAdm(f, pasted, &idle)
	if _, err := f.Report("a", "done", "first"); err != nil {
		t.Fatal(err)
	}
	if len(*pasted) != 0 {
		t.Fatalf("woke a working admiral: %q", *pasted)
	}
	f.Mail().Inbox(mail.Supervisor, false, 0)
	idle = true
	if _, err := f.Report("a", "progress", "halfway"); err != nil {
		t.Fatal(err)
	}
	if len(*pasted) != 0 {
		t.Fatalf("progress woke the admiral: %q", *pasted)
	}
	f.Mail().Inbox(mail.Supervisor, false, 0)
	f.Report("a", "blocked", "need creds")
	f.Report("b", "question", "which db?")
	if want := "%sup " + ReportLine("a", "blocked", "need creds"); len(*pasted) != 1 || (*pasted)[0] != want {
		t.Fatalf("pasted %q, want one %q", *pasted, want)
	}
	f.Mail().Inbox(mail.Supervisor, false, 0)
	f.Report("b", "question", "which db?")
	if len(*pasted) != 2 {
		t.Fatalf("new batch did not wake: %q", *pasted)
	}
	if _, err := f.Report("ghost", "done", "x"); err == nil {
		t.Fatal("report from an agent not on the board")
	}
}

func TestReportSurvivesRefresh(t *testing.T) {
	f, _ := mailFleet(t)
	if _, err := f.Report("a", "done", "shipped\nit"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	r := f.Store.Load().Agents["a"].Report
	if r == nil || r.State != "done" || r.Summary != "shipped it" || r.At.IsZero() {
		t.Fatalf("report after refresh: %+v", r)
	}
}

func TestAdmiralIsReservedName(t *testing.T) {
	for _, n := range []string{"admiral", "supervisor"} {
		if validName(n) {
			t.Fatalf("%q must be reserved", n)
		}
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// repo returns a temp git repo with one commit on main.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644)
	git(t, dir, "add", "f")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestCloseBlocker(t *testing.T) {
	if r := CloseBlocker(t.TempDir()); r != "not a git work tree" {
		t.Fatalf("plain dir: %q", r)
	}
	dir := repo(t)
	if r := CloseBlocker(dir); r != "" {
		t.Fatalf("clean merged: %q", r)
	}
	os.WriteFile(filepath.Join(dir, "g"), []byte("y"), 0o644)
	if r := CloseBlocker(dir); r != "uncommitted changes" {
		t.Fatalf("dirty: %q", r)
	}
	git(t, dir, "checkout", "-q", "-b", "feat")
	git(t, dir, "add", "g")
	git(t, dir, "commit", "-q", "-m", "feat")
	if r := CloseBlocker(dir); r != "not merged into main" {
		t.Fatalf("unmerged: %q", r)
	}
	ms := repo(t)
	git(t, ms, "branch", "-m", "main", "master")
	if r := CloseBlocker(ms); r != "" {
		t.Fatalf("master fallback: %q", r)
	}
}

func TestAutoCloseDone(t *testing.T) {
	f, pasted := mailFleet(t)
	fakeAdm(f, pasted, ptr(true))
	var closed []string
	f.CloseLater = func(n string) error { closed = append(closed, n); return nil }
	clean, dirty := repo(t), repo(t)
	os.WriteFile(filepath.Join(dirty, "g"), []byte("y"), 0o644)
	setDir := func(name, dir string) {
		f.Store.Update(func(b *board.Board) error { b.Agents[name].Dir = dir; return nil })
	}
	setDir("a", clean)
	setDir("b", dirty)

	// Flag off: never closes.
	if note, _ := f.Report("a", "done", "x"); note != "" || len(closed) != 0 {
		t.Fatalf("flag off: note %q closed %v", note, closed)
	}

	cfg := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfg, []byte("[defaults]\nauto_close_done = true\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	pr, err := prefs.Load()
	if err != nil || !pr.For("cat").AutoCloseDone {
		t.Fatalf("prefs: %v", err)
	}
	f.Prefs = pr
	f.Mail().Inbox(mail.Supervisor, false, 0)
	if note, _ := f.Report("b", "done", "y"); note != "not auto-closed: uncommitted changes" || len(closed) != 0 {
		t.Fatalf("dirty: note %q closed %v", note, closed)
	}
	if last := (*pasted)[len(*pasted)-1]; !strings.HasSuffix(last, "Full text: call fleet_reports. (not auto-closed: uncommitted changes)") {
		t.Fatalf("admiral line: %q", last)
	}
	if note, _ := f.Report("a", "blocked", "z"); note != "" || len(closed) != 0 {
		t.Fatalf("blocked must not close: %q %v", note, closed)
	}
	if note, _ := f.Report("a", "done", "z"); note != "auto-closing" || len(closed) != 1 || closed[0] != "a" {
		t.Fatalf("clean merged: note %q closed %v", note, closed)
	}
}

// The real admiral lookup: the @role=sup pane in fleet:main, classified
// with the admiral profile's patterns.
func TestSupPaneIdleByPatterns(t *testing.T) {
	f, _ := mailFleet(t)
	if v := f.supPane(); v.Pane != "" {
		t.Fatalf("no main window, got pane %q", v.Pane)
	}
	supervisor.Save(f.Home, supervisor.Session{Profile: "cat"}) // patterns "fake"
	pane, err := f.T.Spawn(Session, "main", f.Home, nil, "sh -c 'printf \"> \"; cat'")
	if err != nil {
		t.Fatal(err)
	}
	f.T.SetPaneOption(pane, "@role", "sup")
	var idle bool
	for i := 0; i < 50 && !idle; i++ {
		time.Sleep(50 * time.Millisecond)
		idle = f.supPane().Idle
	}
	if v := f.supPane(); v.Pane != pane || !v.Idle || !v.Box || v.Input != "" {
		t.Fatalf("sup pane %+v, want %q idle with an empty box", v, pane)
	}
}

// A long report keeps its full text for fleet_reports after the agent is
// closed; the board and the wake line carry only a short headline.
func TestLongReportSurvivesClose(t *testing.T) {
	f, pasted := mailFleet(t)
	fakeAdm(f, pasted, ptr(true))
	full := "Heads-up: " + strings.Repeat("overlap detail ", 30) + "\nsecond line: branch slice/x"
	raw := full + "\x1b[2J\x03"
	if _, err := f.Report("a", "done", raw); err != nil {
		t.Fatal(err)
	}
	if err := f.Close("a"); err != nil {
		t.Fatal(err)
	}
	ms, err := f.Mail().Inbox(mail.Supervisor, false, 0)
	if err != nil || len(ms) != 1 || ms[0].Text != "done: "+strings.TrimSpace(strings.ReplaceAll(full, " \n", "\n"))+"[2J" {
		t.Fatalf("full text lost: %+v %v", ms, err)
	}
	if len(*pasted) != 1 || !strings.Contains((*pasted)[0], "Full text: call fleet_reports.") || len([]rune((*pasted)[0])) > 180 || strings.ContainsAny((*pasted)[0], "\n\x1b\x03") {
		t.Fatalf("wake line: %q", *pasted)
	}
}

// Only the agent's own work blocks auto-close: dirt that predates its
// spawn and an unchanged HEAD don't; a non-git dir never does.
func TestAgentBlockerBaseline(t *testing.T) {
	if r := AgentBlocker(&board.Agent{Dir: t.TempDir()}); r != "" {
		t.Fatalf("plain dir: %q", r)
	}
	if r := AgentBlocker(&board.Agent{Dir: filepath.Join(t.TempDir(), "gone")}); r != "" {
		t.Fatalf("removed dir: %q", r)
	}
	dir := repo(t)
	os.WriteFile(filepath.Join(dir, "f"), []byte("theirs"), 0o644)
	os.WriteFile(filepath.Join(dir, "old"), []byte("theirs"), 0o644)
	git(t, dir, "checkout", "-q", "-b", "feat") // unmerged-looking, but not the agent's
	a := &board.Agent{Dir: dir, Baseline: Baseline(dir)}
	if a.Baseline == nil || len(a.Baseline.Dirty) != 2 {
		t.Fatalf("baseline: %+v", a.Baseline)
	}
	if r := AgentBlocker(a); r != "" {
		t.Fatalf("read-only agent in a dirty tree: %q", r)
	}
	if r := AgentBlocker(&board.Agent{Dir: dir}); r != "uncommitted changes" {
		t.Fatalf("no baseline keeps the strict rule: %q", r)
	}
	os.WriteFile(filepath.Join(dir, "f"), []byte("mine"), 0o644)
	if r := AgentBlocker(a); r != "uncommitted changes by the agent: f" {
		t.Fatalf("edit to a dirty file: %q", r)
	}
	os.WriteFile(filepath.Join(dir, "f"), []byte("theirs"), 0o644)
	os.MkdirAll(filepath.Join(dir, "d"), 0o755)
	os.WriteFile(filepath.Join(dir, "d", "new"), []byte("x"), 0o644)
	if r := AgentBlocker(a); r != "uncommitted changes by the agent: d/new" {
		t.Fatalf("new file: %q", r)
	}
	os.RemoveAll(filepath.Join(dir, "d"))
	a.Name = "agent"
	other := filepath.Join(t.TempDir(), "sibling")
	git(t, dir, "worktree", "add", "-q", other, "-b", "sibling")
	os.WriteFile(filepath.Join(other, "s"), []byte("x"), 0o644)
	if r := AgentBlocker(a); r != "" {
		t.Fatalf("a sibling's worktree blocked: %q", r)
	}
	wt := filepath.Join(t.TempDir(), "agent")
	git(t, dir, "worktree", "add", "-q", wt, "-b", "agent")
	if r := AgentBlocker(a); r != "" {
		t.Fatalf("own clean, merged worktree blocked: %q", r)
	}
	os.WriteFile(filepath.Join(wt, "w"), []byte("x"), 0o644)
	if r := AgentBlocker(a); r != "worktree left behind (uncommitted changes): "+wt {
		t.Fatalf("own dirty worktree: %q", r)
	}
	git(t, dir, "worktree", "remove", "--force", wt)
	git(t, dir, "add", "old")
	git(t, dir, "commit", "-q", "-m", "agent commit")
	if r := AgentBlocker(a); r != "not merged into main" {
		t.Fatalf("own unmerged commit: %q", r)
	}
	git(t, dir, "checkout", "-q", "main")
	if r := AgentBlocker(a); r != "" {
		t.Fatalf("back on merged main: %q", r)
	}
}

func TestAutoCloseReadOnlyAndNonGit(t *testing.T) {
	f, pasted := mailFleet(t)
	fakeAdm(f, pasted, ptr(true))
	var closed []string
	f.CloseLater = func(n string) error { closed = append(closed, n); return nil }
	// worktree = false: this test checks the spawn dir's own baseline, so
	// the agent works in the repo rather than a worktree of its own.
	cfg := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfg, []byte("[defaults]\nauto_close_done = true\nworktree = false\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	f.Prefs, _ = prefs.Load()

	// a: non-git dir (mailFleet spawns in a temp home).
	if note, _ := f.Report("a", "done", "review"); note != "auto-closing" || len(closed) != 1 {
		t.Fatalf("non-git: %q %v", note, closed)
	}
	// b: spawned into a repo with someone else's uncommitted change.
	dir := repo(t)
	os.WriteFile(filepath.Join(dir, "f"), []byte("theirs"), 0o644)
	if _, err := f.Spawn("c", "cat", dir, ""); err != nil {
		t.Fatal(err)
	}
	if note, _ := f.Report("c", "done", "review"); note != "auto-closing" || len(closed) != 2 {
		t.Fatalf("pre-existing dirt: %q %v", note, closed)
	}
	os.WriteFile(filepath.Join(dir, "g"), []byte("mine"), 0o644)
	f.Mail().Inbox(mail.Supervisor, false, 0)
	note, _ := f.Report("c", "done", "again")
	if note != "not auto-closed: uncommitted changes by the agent: g" || len(closed) != 2 {
		t.Fatalf("own change: %q %v", note, closed)
	}
	if last := (*pasted)[len(*pasted)-1]; !strings.HasSuffix(last, "("+note+")") {
		t.Fatalf("admiral line lacks the reason: %q", last)
	}
}

// A report that lands while the admiral is mid-turn is queued and typed
// once the UI poll sees the admiral idle.
func TestWakeQueuedWhileAdmiralBusy(t *testing.T) {
	f, pasted := mailFleet(t)
	idle := false
	fakeAdm(f, pasted, &idle)
	f.Report("a", "done", "first")
	f.DeliverWake()
	if len(*pasted) != 0 {
		t.Fatalf("typed into a busy admiral: %q", *pasted)
	}
	f.Report("b", "question", "which db?")
	idle = true
	f.DeliverWake()
	f.DeliverWake()
	if want := "%sup [fleet] 2 new reports: a (done), b (question). Full text: call fleet_reports."; len(*pasted) != 1 || (*pasted)[0] != want {
		t.Fatalf("pasted %q, want one %q", *pasted, want)
	}
	// Read before the admiral went idle: nothing stale is typed.
	idle = false
	f.Report("a", "done", "second")
	f.Mail().Inbox(mail.Supervisor, false, 0)
	idle = true
	f.DeliverWake()
	if len(*pasted) != 1 {
		t.Fatalf("stale wake typed: %q", *pasted)
	}
}

func TestIdleDone(t *testing.T) {
	now := time.Now()
	at := now.Add(-30 * time.Minute)
	a := &board.Agent{Status: "idle", StatusSince: at.Add(time.Minute), Report: &board.Report{State: "done", At: at}}
	if !idleDone(a, 15*time.Minute, now) {
		t.Fatal("done and idle 29m")
	}
	if idleDone(a, 40*time.Minute, now) {
		t.Fatal("not idle long enough")
	}
	a.StatusSince = at.Add(10 * time.Minute) // worked again after reporting
	if idleDone(a, 15*time.Minute, now) {
		t.Fatal("went idle long after the report")
	}
	a.StatusSince, a.Report.State = at, "blocked"
	if idleDone(a, 15*time.Minute, now) {
		t.Fatal("blocked")
	}
}

func TestSweepDoneClosesIdleDone(t *testing.T) {
	f, _ := mailFleet(t)
	closed := make(chan string, 4)
	f.CloseLater = func(n string) error { closed <- n; return nil }
	cfg := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfg, []byte("[defaults]\nauto_close_done = true\nauto_close_idle = \"10m\"\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	f.Prefs, _ = prefs.Load()
	now := time.Now()
	at := now.Add(-20 * time.Minute)
	as := []*board.Agent{
		{Name: "a", Profile: "cat", Dir: t.TempDir(), Status: "idle", StatusSince: at, Report: &board.Report{State: "done", At: at}},
		{Name: "b", Profile: "cat", Dir: t.TempDir(), Status: "working", StatusSince: at, Report: &board.Report{State: "done", At: at}},
	}
	f.sweepDone(as, now)
	select {
	case n := <-closed:
		if n != "a" {
			t.Fatalf("closed %q", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("idle done agent not closed")
	}
	f.sweep.run.Lock() // wait for the sweep to finish
	f.sweep.run.Unlock()
	f.sweepDone(as, now.Add(time.Second)) // rate-limited
	if len(closed) != 0 {
		t.Fatalf("extra close: %d", len(closed))
	}
}
