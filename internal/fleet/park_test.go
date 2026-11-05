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
	"github.com/sturlabragason/fleet/internal/tmux"
)

// resumable prints "mode=<arg> <arg>" so a captured pane shows whether the
// process was launched fresh or resumed, and whether the starting mode's
// args came with it, then waits like a real agent.
const resumable = `[res]
command = "sh"
args = ["-c", "echo mode=${1:-none} ${2:-none}; echo '\u2714 Done'; cat", "sh"]
mode_args = { auto = "--auto" }
launch_id = ["started-{id}"]
resume = ["resumed-{id}"]
patterns = "fake"
`

// parkFleet is a Fleet on its own fleet-test-* socket with the resumable
// profile and cfg as the live config.toml (pinned via FLEET_CONFIG).
func parkFleet(t *testing.T, cfg string) (*Fleet, string) {
	t.Helper()
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(resumable+"\n[cat]\ncommand = \"sh\"\nargs = [\"-c\", \"cat\"]\npatterns = \"fake\"\n"), 0o644)
	path := filepath.Join(home, "config.toml")
	os.WriteFile(path, []byte(cfg), 0o644)
	t.Setenv("FLEET_CONFIG", path)
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	return f, path
}

func setState(t *testing.T, f *Fleet, name, st string, since time.Time) {
	t.Helper()
	err := f.Store.Update(func(b *board.Board) error {
		a, ok := b.Agents[name]
		if !ok {
			return fmt.Errorf("no agent %q", name)
		}
		a.Status, a.StatusSince = st, since
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// board-only agent: the guard's selection does not need a live process.
func putAgent(t *testing.T, f *Fleet, name, st string, idle time.Duration, session string) {
	t.Helper()
	err := f.Store.Update(func(b *board.Board) error {
		b.Agents[name] = &board.Agent{Name: name, Status: st, StatusSince: time.Now().Add(-idle),
			SessionID: session, Profile: "res", Dir: f.Home, Created: time.Now().Add(-idle)}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// guard runs one memory-guard check with the reading Refresh would take.
func guard(t *testing.T, f *Fleet, now time.Time) {
	t.Helper()
	low, free, m := f.LowMemory()
	f.guardMemory(boardList(f), now, low, free, m)
}

// boardList is the guard's input: every agent on the board.
func boardList(f *Fleet) []*board.Agent {
	var out []*board.Agent
	for _, a := range f.Store.Load().Agents {
		out = append(out, a)
	}
	return out
}

func waitPane(t *testing.T, f *Fleet, name string) string {
	t.Helper()
	for i := 0; i < 50; i++ {
		if p := f.paneOf(name); p != "" {
			return p
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s never got a pane", name)
	return ""
}

func TestParkKeepsSessionAndWakeResumesIt(t *testing.T) {
	f, _ := parkFleet(t, "")
	a, err := f.Spawn("p1", "res", f.Home, "")
	if err != nil {
		t.Fatal(err)
	}
	id, created := a.SessionID, a.Created
	if id == "" {
		t.Fatal("no session id")
	}
	pane := waitPane(t, f, "p1")
	setState(t, f, "p1", "idle", time.Now())

	if err := f.Park("p1", ""); err != nil {
		t.Fatal(err)
	}
	got := f.Store.Load().Agents["p1"]
	if got == nil {
		t.Fatal("parking dropped the agent from the board")
	}
	if got.Status != "parked" || got.SessionID != id || got.PaneID != "" || !strings.Contains(got.LastLine, "fleet wake p1") {
		t.Fatalf("parked agent: %+v", got)
	}
	if f.paneOf("p1") != "" {
		t.Error("parked agent still has a pane")
	}
	if _, err := f.T.Capture(pane, 5); err == nil {
		t.Error("parked process still running")
	}
	// Refresh must keep it parked, not report it gone.
	f.DrainQueue, f.Housekeeping = true, true
	f.Refresh()
	if st := f.Store.Load().Agents["p1"].Status; st != "parked" {
		t.Fatalf("after refresh: %s", st)
	}
	// A new spawn must not quietly take the parked name.
	if _, err := f.Spawn("p1", "res", f.Home, ""); err == nil || !strings.Contains(err.Error(), "parked") {
		t.Errorf("spawn over a parked name: %v", err)
	}

	woke, err := f.Wake("p1", false)
	if err != nil {
		t.Fatal(err)
	}
	if woke.SessionID != id || woke.Status == "parked" || !woke.Created.Equal(created) {
		t.Fatalf("woke agent: %+v", woke)
	}
	pane = waitPane(t, f, "p1")
	var out string
	for i := 0; i < 30 && !strings.Contains(out, "mode="); i++ {
		time.Sleep(100 * time.Millisecond)
		out, _ = f.T.Capture(pane, 10)
	}
	if !strings.Contains(out, "mode=resumed-"+id) {
		t.Errorf("pane did not resume the session: %q", out)
	}
	if _, err := f.Wake("p1", false); err == nil {
		t.Error("waking a running agent must fail")
	}
}

// A woken agent is a fresh process, so it needs the starting mode again:
// without it an agy agent comes back asking for permission (ADR-56).
func TestWakeResumesInTheConfiguredMode(t *testing.T) {
	f, _ := parkFleet(t, "[defaults]\nmode = \"auto\"\n")
	a, err := f.Spawn("m1", "res", f.Home, "")
	if err != nil {
		t.Fatal(err)
	}
	id := a.SessionID
	waitFor(t, f, "m1", "mode=--auto started-"+id)
	setState(t, f, "m1", "idle", time.Now())
	if err := f.Park("m1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Wake("m1", false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, f, "m1", "mode=--auto resumed-"+id)
}

// waitFor fails unless the agent's pane shows want within a few seconds.
func waitFor(t *testing.T, f *Fleet, name, want string) {
	t.Helper()
	pane, out := waitPane(t, f, name), ""
	for i := 0; i < 50; i++ {
		if out, _ = f.T.Capture(pane, 10); strings.Contains(out, want) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s: pane never showed %q: %q", name, want, out)
}

func TestParkRefusesAgentsThatAreNotIdle(t *testing.T) {
	f, _ := parkFleet(t, "")
	putAgent(t, f, "working", "working", time.Hour, "s1")
	putAgent(t, f, "waiting", "waiting", time.Hour, "s2")
	putAgent(t, f, "starting", "starting", time.Hour, "s3")
	putAgent(t, f, "fresh", "idle", time.Minute, "")
	for _, name := range []string{"working", "waiting", "starting", "fresh", "ghost"} {
		if err := f.Park(name, ""); err == nil {
			t.Errorf("parked %s", name)
		} else if st := f.Store.Load().Agents[name]; st != nil && st.Status == "parked" {
			t.Errorf("%s parked despite the error", name)
		}
	}
	if _, err := f.Wake("working", false); err == nil {
		t.Error("woke an agent that was never parked")
	}
}

func TestGuardParksLongestIdleFirstAndTellsTheAdmiral(t *testing.T) {
	f, _ := parkFleet(t, "[memory]\nmin_free = \"4GB\"\ncheck_every = \"1ms\"\n")
	idle := true
	fakeAdm(f, nil, &idle)
	f.FreeMem = func() (uint64, error) { return 1 << 30, nil }
	putAgent(t, f, "old", "idle", time.Hour, "s1")
	putAgent(t, f, "new", "idle", time.Minute, "s2")
	putAgent(t, f, "busy", "working", 2*time.Hour, "s3")
	putAgent(t, f, "ask", "waiting", 2*time.Hour, "s4")
	putAgent(t, f, "noid", "idle", 3*time.Hour, "")

	status := func() map[string]string {
		out := map[string]string{}
		for n, a := range f.Store.Load().Agents {
			out[n] = a.Status
		}
		return out
	}
	guard(t, f, time.Now())
	if s := status(); s["old"] != "parked" || s["new"] == "parked" {
		t.Fatalf("first check parked the wrong agent: %v", s)
	}
	time.Sleep(5 * time.Millisecond)
	guard(t, f, time.Now())
	s := status()
	if s["new"] != "parked" {
		t.Fatalf("second check: %v", s)
	}
	if s["busy"] != "working" || s["ask"] != "waiting" || s["noid"] != "idle" {
		t.Fatalf("guard touched a protected agent: %v", s)
	}
	ms, err := f.Mail().Inbox("admiral", true, 10)
	if err != nil {
		t.Fatal(err)
	}
	var told int
	for _, m := range ms {
		if strings.Contains(m.Text, "to free memory") {
			told++
		}
	}
	if told != 2 {
		t.Errorf("admiral got %d guard reports, want 2 (%+v)", told, ms)
	}
	// Guard off, or enough memory: nothing more is parked.
	putAgent(t, f, "spare", "idle", time.Hour, "s5")
	f.FreeMem = func() (uint64, error) { return 8 << 30, nil }
	time.Sleep(5 * time.Millisecond)
	guard(t, f, time.Now())
	if status()["spare"] != "idle" {
		t.Error("parked with memory to spare")
	}
	f.FreeMem = func() (uint64, error) { return 1 << 30, nil }
	os.WriteFile(filepath.Join(f.Home, "config.toml"), []byte("[memory]\nguard = false\n"), 0o644)
	time.Sleep(5 * time.Millisecond)
	guard(t, f, time.Now())
	if status()["spare"] != "idle" {
		t.Error("guard = false did not switch the guard off")
	}
}

func TestGuardRateLimitsItself(t *testing.T) {
	f, _ := parkFleet(t, "[memory]\nmin_free = \"4GB\"\ncheck_every = \"1h\"\n")
	idle := true
	fakeAdm(f, nil, &idle)
	f.FreeMem = func() (uint64, error) { return 1 << 30, nil }
	putAgent(t, f, "a", "idle", 2*time.Hour, "s1")
	putAgent(t, f, "b", "idle", time.Hour, "s2")
	now := time.Now()
	guard(t, f, now)
	guard(t, f, now.Add(time.Minute))
	if st := f.Store.Load().Agents["b"].Status; st != "idle" {
		t.Errorf("guard parked twice inside check_every: b is %s", st)
	}
	// A second process shares the stamp through the same file.
	g, _ := New(f.T, f.Home, "/nonexistent")
	if ok, _ := g.claimGuard(now.Add(2*time.Minute), time.Hour); ok {
		t.Error("another process ignored the shared rate limit")
	}
}

func TestLowMemoryQueuesSpawnsAndHoldsTheQueue(t *testing.T) {
	f, _ := parkFleet(t, "[memory]\nmin_free = \"4GB\"\ncheck_every = \"1h\"\n")
	idle := true
	fakeAdm(f, nil, &idle)
	f.FreeMem = func() (uint64, error) { return 8 << 30, nil }
	if _, err := f.Spawn("keep", "cat", f.Home, ""); err != nil {
		t.Fatal(err)
	}
	waitPane(t, f, "keep")
	f.FreeMem = func() (uint64, error) { return 1 << 30, nil }
	a, err := f.Spawn("q1", "cat", f.Home, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != "queued" || !strings.Contains(a.LastLine, "memory guard") {
		t.Fatalf("spawn while low: %+v", a)
	}
	f.DrainQueue, f.DrainSync = true, true
	f.Refresh()
	if st := f.Store.Load().Agents["q1"].Status; st != "queued" {
		t.Fatalf("queue drained while memory was low: %s", st)
	}
	f.FreeMem = func() (uint64, error) { return 8 << 30, nil }
	f.Refresh()
	if st := f.Store.Load().Agents["q1"].Status; st == "queued" {
		t.Fatal("queue did not drain once memory came back")
	}
	waitPane(t, f, "q1")
}

func TestMaxWorkingAndGuardAreReadLive(t *testing.T) {
	f, cfg := parkFleet(t, "[defaults]\nmax_working = 1\n")
	if f.MaxWorking() != 1 || !f.MemPrefs().Guard {
		t.Fatalf("start: max_working %d memory %+v", f.MaxWorking(), f.MemPrefs())
	}
	os.WriteFile(cfg, []byte("[defaults]\nmax_working = 4\n\n[memory]\nguard = false\nmin_free = \"2GB\"\n"), 0o644)
	if f.MaxWorking() != 4 {
		t.Errorf("max_working after edit: %d", f.MaxWorking())
	}
	if m := f.MemPrefs(); m.Guard || m.MinFree != 2<<30 {
		t.Errorf("[memory] after edit: %+v", m)
	}
	// A broken file keeps the settings the fleet started with.
	os.WriteFile(cfg, []byte("[defaults\nmax_working = "), 0o644)
	if f.MaxWorking() != 1 {
		t.Errorf("broken config: max_working %d, want the startup value 1", f.MaxWorking())
	}
}

func TestDrainingTwiceStartsOneProcess(t *testing.T) {
	f, _ := parkFleet(t, "[memory]\nmin_free = \"4GB\"\ncheck_every = \"1h\"\n")
	idle := true
	fakeAdm(f, nil, &idle)
	f.FreeMem = func() (uint64, error) { return 8 << 30, nil }
	if _, err := f.Spawn("d1", "cat", f.Home, ""); err != nil {
		t.Fatal(err)
	}
	waitPane(t, f, "d1")
	f.FreeMem = func() (uint64, error) { return 1 << 30, nil }
	if a, err := f.Spawn("d2", "cat", f.Home, ""); err != nil || a.Status != "queued" {
		t.Fatalf("d2: %v %+v", err, a)
	}
	// Two processes drain the same queue at once (the overview and an MCP
	// server): the flock-ed claim must let exactly one start d2.
	g, err := New(f.T, f.Home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	f.FreeMem = func() (uint64, error) { return 8 << 30, nil }
	g.FreeMem = f.FreeMem
	f.DrainQueue, f.DrainSync = true, true
	g.DrainQueue, g.DrainSync = true, true
	var wg sync.WaitGroup
	for _, x := range []*Fleet{f, g} {
		wg.Add(1)
		go func(x *Fleet) { defer wg.Done(); x.Refresh() }(x)
	}
	wg.Wait()
	waitPane(t, f, "d2")
	f.Refresh()
	g.Refresh()
	var n int
	ps, err := f.T.ListPanes("")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.Agent == "d2" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("d2 has %d panes, want 1", n)
	}
}

func TestWakeWaitsForMemoryUnlessForced(t *testing.T) {
	f, _ := parkFleet(t, "[memory]\nmin_free = \"4GB\"\ncheck_every = \"1h\"\n")
	f.FreeMem = func() (uint64, error) { return 8 << 30, nil }
	if _, err := f.Spawn("w1", "res", f.Home, ""); err != nil {
		t.Fatal(err)
	}
	waitPane(t, f, "w1")
	setState(t, f, "w1", "idle", time.Now())
	if err := f.Park("w1", ""); err != nil {
		t.Fatal(err)
	}
	f.FreeMem = func() (uint64, error) { return 1 << 30, nil }
	if _, err := f.Wake("w1", false); err == nil {
		t.Error("woke into a low-memory machine")
	} else if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error should offer --force: %v", err)
	}
	if st := f.Store.Load().Agents["w1"].Status; st != "parked" {
		t.Fatalf("a refused wake left it %s", st)
	}
	if _, err := f.Wake("w1", true); err != nil {
		t.Fatal(err)
	}
	waitPane(t, f, "w1")
}

func TestTwoWakesStartOneProcess(t *testing.T) {
	f, _ := parkFleet(t, "")
	f.FreeMem = func() (uint64, error) { return 64 << 30, nil }
	if _, err := f.Spawn("w2", "res", f.Home, ""); err != nil {
		t.Fatal(err)
	}
	waitPane(t, f, "w2")
	setState(t, f, "w2", "idle", time.Now())
	if err := f.Park("w2", ""); err != nil {
		t.Fatal(err)
	}
	g, err := New(f.T, f.Home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	g.FreeMem = f.FreeMem
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, x := range []*Fleet{f, g} {
		wg.Add(1)
		go func(i int, x *Fleet) { defer wg.Done(); _, errs[i] = x.Wake("w2", false) }(i, x)
	}
	wg.Wait()
	if errs[0] == nil && errs[1] == nil {
		t.Error("both wakes claimed the same parked agent")
	}
	waitPane(t, f, "w2")
	var n int
	ps, err := f.T.ListPanes("")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.Agent == "w2" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("w2 has %d panes, want 1", n)
	}
}

func TestParkWithoutAPaneStillParks(t *testing.T) {
	f, _ := parkFleet(t, "")
	f.FreeMem = func() (uint64, error) { return 64 << 30, nil }
	if _, err := f.Spawn("s1", "res", f.Home, ""); err != nil {
		t.Fatal(err)
	}
	pane := waitPane(t, f, "s1")
	setState(t, f, "s1", "idle", time.Now())
	// A pane that is already gone is not a prompt: parking records the
	// state the agent is in, it does not need a screen to kill.
	f.T.KillPane(pane)
	for i := 0; i < 30 && f.paneOf("s1") != ""; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if err := f.Park("s1", ""); err != nil {
		t.Fatalf("a gone pane is not a prompt: %v", err)
	}
}

func TestParkRefusesWhenItCannotReadTheScreen(t *testing.T) {
	f, _ := parkFleet(t, "")
	f.FreeMem = func() (uint64, error) { return 64 << 30, nil }
	if _, err := f.Spawn("pr1", "res", f.Home, ""); err != nil {
		t.Fatal(err)
	}
	pane := waitPane(t, f, "pr1")
	// The agent is on a y/n prompt while the board still says idle: the
	// screen, not the status, is what stops the park.
	f.T.Paste(pane, "Continue? [y/n]", true)
	setState(t, f, "pr1", "idle", time.Now())
	var err error
	for i := 0; i < 30; i++ {
		if err = f.Park("pr1", ""); err != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err == nil || !strings.Contains(err.Error(), "prompt") {
		t.Fatalf("parked an agent on a prompt: %v", err)
	}
	if st := f.Store.Load().Agents["pr1"].Status; st == "parked" {
		t.Error("a refused park still wrote parked")
	}
}

func TestWakeRefusesWhileThePaneIsAlive(t *testing.T) {
	f, _ := parkFleet(t, "")
	f.FreeMem = func() (uint64, error) { return 64 << 30, nil }
	if _, err := f.Spawn("wa1", "res", f.Home, ""); err != nil {
		t.Fatal(err)
	}
	waitPane(t, f, "wa1")
	// A board entry that says parked while the process is still alive (a
	// park that did not take): waking would start a second one.
	setState(t, f, "wa1", "parked", time.Now())
	if _, err := f.Wake("wa1", false); err == nil {
		t.Fatal("woke an agent whose pane was still alive")
	}
	// Refresh reads the live pane and drops the stale label.
	f.DrainQueue, f.Housekeeping = true, true
	f.Refresh()
	if st := f.Store.Load().Agents["wa1"].Status; st == "parked" {
		t.Errorf("a live pane stayed parked: %s", st)
	}
}
