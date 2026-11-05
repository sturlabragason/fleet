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
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/tmux"
)

func TestNormalizeCardDefaultsAndEmpty(t *testing.T) {
	if c := NormalizeCard(nil); c != nil {
		t.Fatalf("nil card: %+v", c)
	}
	if c := NormalizeCard(&board.Card{Goal: "  ", Check: "", Result: " "}); c != nil {
		t.Fatalf("empty card is a card: %+v", c)
	}
	c := NormalizeCard(&board.Card{Goal: " ship it ", Check: " go test ./... ", Want: 0})
	if c.Goal != "ship it" || c.Check != "go test ./..." {
		t.Fatalf("not trimmed: %+v", c)
	}
	if c.Result != DefaultResult {
		t.Fatalf("result %q, want %q", c.Result, DefaultResult)
	}
	if c := NormalizeCard(&board.Card{Result: "out/notes.md", Want: 2}); c.Result != "out/notes.md" || c.Want != 2 {
		t.Fatalf("named result: %+v", c)
	}
	// A card never arrives pre-checked.
	if c := NormalizeCard(&board.Card{Goal: "g", Checked: &board.Check{Pass: true}}); c.Checked != nil {
		t.Fatalf("kept a stale verdict: %+v", c.Checked)
	}
}

func TestCardPromptTellsGoalCheckAndResult(t *testing.T) {
	if CardPrompt(nil) != "" {
		t.Fatal("prompt for no card")
	}
	p := CardPrompt(NormalizeCard(&board.Card{Goal: "ship it", Check: "make test", Want: 1}))
	for _, want := range []string{"ship it", "make test", "must exit 1", DefaultResult} {
		if !strings.Contains(p, want) {
			t.Fatalf("card prompt missing %q:\n%s", want, p)
		}
	}
}

// cardFleet is a Fleet like queueFleet's (max_working 1, the cheap "cat"
// profile) with a short ready_timeout: a carded spawn always has a first
// prompt to paste, so it waits for the pane to look ready.
func cardFleet(t *testing.T) *Fleet {
	t.Helper()
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte("[cat]\ncommand = \"sh\"\nargs = [\"-c\", \"cat\"]\npatterns = \"fake\"\n"), 0o644)
	cfg := filepath.Join(home, "config.toml")
	os.WriteFile(cfg, []byte("[defaults]\nmax_working = 1\nready_timeout = \"100ms\"\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSpawnStoresCardAndQueueKeepsIt(t *testing.T) {
	f := cardFleet(t)
	card := &board.Card{Goal: "ship it", Check: "true"}
	if _, err := f.SpawnWith(SpawnOpts{Card: card}, "a1", "cat", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	got := f.Store.Load().Agents["a1"].Card
	if got == nil || got.Goal != "ship it" || got.Check != "true" || got.Result != DefaultResult {
		t.Fatalf("card on the board: %+v", got)
	}
	// The agent is told its card: the block reaches the pane as part of the
	// first prompt (the cat profile echoes what is typed into it).
	first, err := f.T.Capture(f.Store.Load().Agents["a1"].PaneID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Task card", "ship it", "`true`", DefaultResult} {
		if !strings.Contains(first, want) {
			t.Fatalf("first prompt missing %q:\n%s", want, first)
		}
	}
	// A spawn with no card leaves the field nil: unchanged behaviour.
	if _, err := f.SpawnWith(SpawnOpts{}, "a2", "cat", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	q := f.Store.Load().Agents["a2"]
	if q.Status != "queued" || q.Card != nil {
		t.Fatalf("uncarded spawn: %+v", q)
	}
	// A queued spawn keeps its card for the drain.
	if _, err := f.SpawnWith(SpawnOpts{Card: &board.Card{Goal: "later", Result: "r.md"}}, "a3", "cat", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	a3 := f.Store.Load().Agents["a3"]
	if a3.Status != "queued" || a3.Card == nil || a3.Card.Result != "r.md" {
		t.Fatalf("queued card: %+v", a3.Card)
	}
}

func TestRunCheckPassFailAndTimeout(t *testing.T) {
	dir := t.TempDir()
	pass := RunCheck(dir, &board.Card{Check: "exit 0"}, time.Minute)
	if !pass.Pass || pass.Code != 0 || pass.Verdict() != "pass (exit 0)" {
		t.Fatalf("pass: %+v", pass)
	}
	fail := RunCheck(dir, &board.Card{Check: "echo nope >&2; exit 3"}, time.Minute)
	if fail.Pass || fail.Code != 3 || !strings.Contains(fail.Out, "nope") {
		t.Fatalf("fail: %+v", fail)
	}
	if v := fail.Verdict(); v != "fail (exit 3, want 0)" {
		t.Fatalf("verdict %q", v)
	}
	// A non-zero expected code is a pass, and zero is then a failure.
	if r := RunCheck(dir, &board.Card{Check: "exit 2", Want: 2}, time.Minute); !r.Pass {
		t.Fatalf("want=2: %+v", r)
	}
	slow := RunCheck(dir, &board.Card{Check: "sleep 30"}, 150*time.Millisecond)
	if slow.Pass || !strings.Contains(slow.Err, "timed out") || slow.Code != -1 {
		t.Fatalf("timeout: %+v", slow)
	}
	if !strings.HasPrefix(slow.Verdict(), "fail (timed out") {
		t.Fatalf("timeout verdict %q", slow.Verdict())
	}
}

func TestRunCheckRunsInTheAgentDir(t *testing.T) {
	dir := t.TempDir()
	r := RunCheck(dir, &board.Card{Check: "pwd"}, time.Minute)
	if !r.Pass || !strings.Contains(r.Out, dir) {
		t.Fatalf("pwd in %s: %+v", dir, r)
	}
}

// setCard puts a card on an agent already on the board.
func setCard(t *testing.T, f *Fleet, name string, c *board.Card) {
	t.Helper()
	if err := f.Store.Update(func(b *board.Board) error {
		b.Agents[name].Card = NormalizeCard(c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDoneReportRunsTheCheckAndRecordsTheVerdict(t *testing.T) {
	f, pasted := mailFleet(t)
	idle := true
	fakeAdm(f, pasted, &idle)
	setCard(t, f, "a", &board.Card{Goal: "ship it", Check: "exit 0"})
	// Go is unset here (the one-shot `fleet report` CLI): the check runs
	// before Report returns, so the note is the auto-close one.
	note, err := f.Report("a", "done", "shipped")
	if err != nil {
		t.Fatal(err)
	}
	if note != "" {
		t.Fatalf("note %q, want the auto-close note (off here)", note)
	}
	ch := f.Store.Load().Agents["a"].Card.Checked
	if ch == nil || !ch.Pass {
		t.Fatalf("verdict: %+v", ch)
	}
	if len(*pasted) != 1 || !strings.Contains((*pasted)[0], "Done-check: pass (exit 0)") {
		t.Fatalf("admiral woken with %q", *pasted)
	}
}

func TestDoneReportReturnsAtOnceWhenTheCheckRunsOffTheLoop(t *testing.T) {
	f, pasted := mailFleet(t)
	idle := true
	fakeAdm(f, pasted, &idle)
	// Stand in for Async(): hold the work instead of running it, so the
	// test sees exactly what the MCP loop sees.
	var held []func()
	f.Go = func(fn func()) { held = append(held, fn) }
	setCard(t, f, "a", &board.Card{Check: "exit 0"})
	note, err := f.Report("a", "done", "shipped")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "done-check running") {
		t.Fatalf("note %q", note)
	}
	if f.Store.Load().Agents["a"].Card.Checked != nil || len(*pasted) != 0 {
		t.Fatal("the check ran on the reporting goroutine")
	}
	if len(held) != 1 {
		t.Fatalf("%d background jobs", len(held))
	}
	held[0]()
	if ch := f.Store.Load().Agents["a"].Card.Checked; ch == nil || !ch.Pass {
		t.Fatalf("verdict after the job ran: %+v", ch)
	}
}

func TestOneDoneCheckRunsOnceAndASweepPicksUpALostOne(t *testing.T) {
	f, pasted := mailFleet(t)
	idle := true
	fakeAdm(f, pasted, &idle)
	out := filepath.Join(t.TempDir(), "runs")
	setCard(t, f, "a", &board.Card{Check: "echo x >> " + out})
	for i := 0; i < 3; i++ {
		if _, err := f.Report("a", "done", "shipped"); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(out)
	if got := strings.Count(string(b), "x"); got != 1 {
		t.Fatalf("the check ran %d times, want 1", got)
	}
	// A runner that died leaves a claim and no verdict: the sweep retries it.
	if err := f.Store.Update(func(bd *board.Board) error {
		c := bd.Agents["a"].Card
		c.Checked, c.Started = nil, time.Now().Add(-2*CheckTimeout)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.sweepChecks(f.Store.Load().Sorted())
	if ch := f.Store.Load().Agents["a"].Card.Checked; ch == nil || !ch.Pass {
		t.Fatalf("sweep verdict: %+v", ch)
	}
	// A fresh claim is not stale, so the sweep leaves it alone.
	if err := f.Store.Update(func(bd *board.Board) error {
		c := bd.Agents["a"].Card
		c.Checked, c.Started = nil, time.Now()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.sweepChecks(f.Store.Load().Sorted())
	if ch := f.Store.Load().Agents["a"].Card.Checked; ch != nil {
		t.Fatalf("sweep took a live claim: %+v", ch)
	}
}

func TestPassingDoneCheckAutoClosesAndFailingOneDoesNot(t *testing.T) {
	for _, tc := range []struct {
		name, check, note string
		closed            bool
	}{
		{"pass", "exit 0", "auto-closing", true},
		{"fail", "exit 1", "not auto-closed: done-check failed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, pasted := mailFleet(t)
			idle := true
			fakeAdm(f, pasted, &idle)
			// auto_close_done is off in mailFleet's config; turn it on for
			// this agent's profile the way the user's config would.
			f.Prefs = autoClosePrefs(t)
			closed := ""
			f.CloseLater = func(n string) error { closed = n; return nil }
			setCard(t, f, "a", &board.Card{Check: tc.check})
			note, err := f.Report("a", "done", "shipped")
			if err != nil {
				t.Fatal(err)
			}
			if note != tc.note {
				t.Fatalf("note %q, want %q", note, tc.note)
			}
			if (closed == "a") != tc.closed {
				t.Fatalf("closed %q, want closed=%v", closed, tc.closed)
			}
			if len(*pasted) != 1 || !strings.Contains((*pasted)[0], tc.note) {
				t.Fatalf("admiral woken with %q", *pasted)
			}
		})
	}
}

func TestRepeatedDoneReportNeverAutoClosesAFailedCheck(t *testing.T) {
	f, pasted := mailFleet(t)
	idle := true
	fakeAdm(f, pasted, &idle)
	f.Prefs = autoClosePrefs(t)
	closed := ""
	f.CloseLater = func(n string) error { closed = n; return nil }
	setCard(t, f, "a", &board.Card{Check: "exit 1"})
	for i := 0; i < 3; i++ {
		note, err := f.Report("a", "done", "shipped")
		if err != nil {
			t.Fatal(err)
		}
		if note != "not auto-closed: done-check failed" {
			t.Fatalf("report %d: note %q", i, note)
		}
	}
	if closed != "" {
		t.Fatalf("auto-closed after a failed check: %s", closed)
	}
}

// autoClosePrefs is a config with auto_close_done on.
func autoClosePrefs(t *testing.T) *prefs.Prefs {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte("[defaults]\nauto_close_done = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_CONFIG", cfg)
	pr, err := prefs.Load()
	if err != nil {
		t.Fatal(err)
	}
	return pr
}

func TestCheckStateAndResultPath(t *testing.T) {
	a := &board.Agent{Name: "a", Dir: t.TempDir()}
	if CheckState(a) != "" {
		t.Fatal("state without a card")
	}
	if _, _, ok := ResultPath(a); ok {
		t.Fatal("result path without a card")
	}
	a.Card = NormalizeCard(&board.Card{Check: "true"})
	if s := CheckState(a); s != "not run yet" {
		t.Fatalf("state %q", s)
	}
	a.Report = &board.Report{State: "done"}
	if s := CheckState(a); s != "running" {
		t.Fatalf("state while checking: %q", s)
	}
	a.Card.Checked = &board.Check{Pass: true, At: time.Now()}
	if s := CheckState(a); !strings.HasPrefix(s, "pass (exit 0) at ") {
		t.Fatalf("state after the check: %q", s)
	}
	p, written, ok := ResultPath(a)
	if !ok || written || !strings.HasSuffix(p, DefaultResult) {
		t.Fatalf("result path %q written=%v ok=%v", p, written, ok)
	}
}
