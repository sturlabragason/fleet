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

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/tmux"
)

type deadRec struct {
	mu             sync.Mutex
	closed, pasted []string
}

func (r *deadRec) get() (closed, pasted []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.closed...), append([]string(nil), r.pasted...)
}

// deadFleet spawns d1 in dir running `sh -c script` as the overview would
// see it (drain and housekeeping on, idle admiral). cfg is the config.toml body ("" =
// none).
func deadFleet(t *testing.T, script, cfg, dir string) (*Fleet, *deadRec) {
	t.Helper()
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(fmt.Sprintf("[dies]\ncommand = \"sh\"\nargs = [\"-c\", %q]\npatterns = \"fake\"\n", script)), 0o644)
	cfgPath := filepath.Join(home, "no-config.toml")
	if cfg != "" {
		cfgPath = filepath.Join(home, "config.toml")
		os.WriteFile(cfgPath, []byte(cfg), 0o644)
	}
	t.Setenv("FLEET_CONFIG", cfgPath)
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	// The memory guard must never read this machine's RAM in a test.
	f.FreeMem = func() (uint64, error) { return 64 << 30, nil }
	r := &deadRec{}
	f.DrainQueue, f.Housekeeping = true, true
	adm := fakeAdm(f, nil, ptr(true))
	adm.onSubmit = func(text string) {
		r.mu.Lock()
		r.pasted = append(r.pasted, text)
		r.mu.Unlock()
	}
	f.CloseLater = func(name string) error { r.mu.Lock(); r.closed = append(r.closed, name); r.mu.Unlock(); return nil }
	if dir == "" {
		dir = home
	}
	if _, err := f.Spawn("d1", "dies", dir, ""); err != nil {
		t.Fatal(err)
	}
	return f, r
}

// poll runs Refresh (and waits for the background reaper) until ok or 5 s.
func poll(t *testing.T, f *Fleet, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		f.Refresh()
		f.dead.run.Lock()
		f.dead.run.Unlock()
		if ok() {
			return
		}
	}
	t.Fatal("timed out")
}

func reports(t *testing.T, f *Fleet) []mail.Msg {
	t.Helper()
	ms, err := f.Mail().Inbox(mail.Supervisor, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func TestDeadPaneCleanExitCloses(t *testing.T) {
	f, r := deadFleet(t, "echo bye; sleep 0.3; exit 0", "", "")
	poll(t, f, func() bool { c, _ := r.get(); return len(c) > 0 })
	for i := 0; i < 3; i++ {
		poll(t, f, func() bool { return true })
	}
	closed, pasted := r.get()
	if len(closed) != 1 || closed[0] != "d1" {
		t.Fatalf("closed %q", closed)
	}
	if len(pasted) != 1 || pasted[0] != "[fleet] d1 exited, closed." {
		t.Fatalf("wake %q", pasted)
	}
	ms := reports(t, f)
	if len(ms) != 1 || ms[0].Text != "done: exited, closed" {
		t.Fatalf("reports %+v", ms)
	}
	if e := f.Store.Load().Agents["d1"].Exit; !e.Clean() || e.At.IsZero() {
		t.Fatalf("exit %+v", e)
	}
}

// The grace is long enough that a slow machine cannot run past it between
// the report and the checks; the test then ends the grace by backdating the
// exit instead of waiting it out.
func TestDeadPaneCrashGrace(t *testing.T) {
	f, r := deadFleet(t, "sleep 0.3; echo boom; echo last words; exit 3", "[defaults]\ndead_pane_grace = \"5s\"\n", "")
	poll(t, f, func() bool { return len(reports(t, f)) > 0 })
	closed, pasted := r.get()
	if len(closed) != 0 {
		t.Fatalf("closed before grace: %q", closed)
	}
	ms := reports(t, f)
	if len(ms) != 1 || !strings.HasPrefix(ms[0].Text, "blocked: crashed (exit 3)\n") || !strings.Contains(ms[0].Text, "boom\nlast words") || strings.Contains(ms[0].Text, "Pane is dead") {
		t.Fatalf("report %+v", ms)
	}
	if want := "[fleet] d1 crashed (exit 3): last words. Closing in 5s. Full text: call fleet_reports."; len(pasted) != 1 || pasted[0] != want {
		t.Fatalf("wake %q, want %q", pasted, want)
	}
	f.Store.Update(func(b *board.Board) error { b.Agents["d1"].Exit.At = time.Now().Add(-time.Hour); return nil })
	poll(t, f, func() bool { c, _ := r.get(); return len(c) == 1 })
	if len(reports(t, f)) != 1 {
		t.Fatal("reported twice")
	}
}

func TestDeadPaneSignalAndOff(t *testing.T) {
	f, r := deadFleet(t, "sleep 0.3; echo boom; kill -TERM $$", "[profile.dies]\ndead_pane_grace = \"OFF\"\n", "")
	poll(t, f, func() bool { return len(reports(t, f)) > 0 })
	ms := reports(t, f)
	if !strings.HasPrefix(ms[0].Text, "blocked: crashed (signal 15)") {
		t.Fatalf("report %q", ms[0].Text)
	}
	f.Store.Update(func(b *board.Board) error { b.Agents["d1"].Exit.At = time.Now().Add(-time.Hour); return nil })
	for i := 0; i < 3; i++ {
		poll(t, f, func() bool { return true })
	}
	closed, pasted := r.get()
	if len(closed) != 0 || len(pasted) != 1 || !strings.Contains(pasted[0], "Not auto-closing (dead_pane_grace = off).") {
		t.Fatalf("closed %q wake %q", closed, pasted)
	}
}

func TestDeadPaneLeftWorkStillCloses(t *testing.T) {
	dir := repo(t)
	f, r := deadFleet(t, "sleep 0.3; echo x > mine.txt; exit 0", "", dir)
	poll(t, f, func() bool { c, _ := r.get(); return len(c) > 0 })
	_, pasted := r.get()
	ms := reports(t, f)
	for _, s := range []string{ms[0].Text, pasted[0]} {
		if !strings.Contains(s, "eft work behind: uncommitted changes by the agent: mine.txt") || !strings.Contains(s, "fleet adopt") {
			t.Fatalf("no work note: %q", s)
		}
	}
}

func TestDeadReportText(t *testing.T) {
	three := 3
	e := &board.Exit{Code: &three}
	_, _, line := deadReport("x", e, "", "", 0)
	if line != "[fleet] x crashed (exit 3). Closing now. Full text: call fleet_reports." {
		t.Fatalf("line %q", line)
	}
	if got := deadReportLine(&board.Exit{}, 5*time.Minute); !strings.Contains(got, "crashed (exit unknown)") || !strings.Contains(got, "Closing in 5m.") {
		t.Fatalf("unknown: %q", got)
	}
	long := strings.Repeat("y", 200)
	if _, _, l := deadReport("x", e, long+"\nPane is dead (status 3)\n", "", 0); !strings.Contains(l, strings.Repeat("y", 79)+"… Closing now.") {
		t.Fatalf("long line: %q", l)
	}
}

func deadReportLine(e *board.Exit, grace time.Duration) string {
	_, _, l := deadReport("x", e, "z", "", grace)
	return l
}

// The parked skip is for acp hosts only, and so is the closing one: a tmux
// agent's dead pane is reported as before.
func TestReapDeadSkipsACPParkAndClose(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "none.toml"))
	f, err := New(&tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	f.CloseLater = func(string) error { return nil }
	now := time.Now()
	as := []*board.Agent{
		{Name: "tmuxparked", Profile: "fake", Status: "parked"},
		{Name: "acpparked", Profile: "fake", Status: "parked", Driver: board.DriverACP},
		{Name: "acpclosing", Profile: "fake", Status: "idle", Driver: board.DriverACP, Closing: now},
		{Name: "acpdied", Profile: "fake", Status: "idle", Driver: board.DriverACP},
	}
	panes := map[string]tmux.PaneInfo{}
	f.Store.Update(func(b *board.Board) error {
		for i, a := range as {
			c := *a
			b.Agents[a.Name] = &c
			panes[a.Name] = tmux.PaneInfo{ID: fmt.Sprintf("%%%d", i), Agent: a.Name, Dead: true, DeadStatus: "0"}
		}
		return nil
	})
	f.reapDead(as, panes, now, false)
	f.dead.run.Lock()
	f.dead.run.Unlock()
	got := map[string]bool{}
	for n, a := range f.Store.Load().Agents {
		got[n] = a.Exit != nil
	}
	want := map[string]bool{"tmuxparked": true, "acpparked": false, "acpclosing": false, "acpdied": true}
	for n, w := range want {
		if got[n] != w {
			t.Errorf("%s reaped %v, want %v", n, got[n], w)
		}
	}
}
