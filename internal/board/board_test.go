package board

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUpdateLoad(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Update(func(b *Board) error { b.Agents["a"] = &Agent{Name: "a", Status: "idle"}; return nil }); err != nil {
		t.Fatal(err)
	}
	if s.Load().Agents["a"].Status != "idle" {
		t.Fatal("not saved")
	}
}

func TestConcurrent(t *testing.T) {
	s := NewStore(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.Update(func(b *Board) error { b.Agents[string(rune('a'+i))] = &Agent{}; return nil })
		}(i)
	}
	wg.Wait()
	if n := len(s.Load().Agents); n != 20 {
		t.Fatalf("lost updates: %d", n)
	}
}

func TestBrokenJSON(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "board.json"), []byte("{nope"), 0o644)
	s := NewStore(dir)
	if len(s.Load().Agents) != 0 {
		t.Fatal("want empty")
	}
	if _, err := os.Stat(filepath.Join(dir, "board.json.broken")); err != nil {
		t.Fatal("no backup")
	}
	// A write must not replace the user's state with an empty board (ADR-113).
	err := s.Update(func(b *Board) error { b.Agents["a"] = &Agent{Name: "a"}; return nil })
	if err == nil || !strings.Contains(err.Error(), "does not parse") {
		t.Fatalf("update over a broken board: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "board.json")); string(data) != "{nope" {
		t.Fatalf("board.json overwritten: %q", data)
	}
	if s.Check() == nil {
		t.Error("Check passed a broken board")
	}
}

// Closed records live in closed.json; an update that never asks for them
// neither reads nor writes that file, and a close moves an agent across in
// one update (ADR-113).
func TestClosedLivesInItsOwnFile(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	now := time.Now()
	s.Update(func(b *Board) error { b.Agents["w1"] = &Agent{Name: "w1", PaneID: "%1"}; return nil })
	if err := s.Update(func(b *Board) error {
		b.AddClosed(b.Agents["w1"], "t", now)
		delete(b.Agents, "w1")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "board.json")); strings.Contains(string(data), "closed") {
		t.Fatalf("board.json still carries closed records: %s", data)
	}
	if c, ok := s.Load().ClosedRecord("w1", now); !ok || c.Team != "t" || c.Agent.PaneID != "" {
		t.Fatalf("record %+v %v", c, ok)
	}
	// A broken closed.json does not stop updates that don't use it, and
	// stops (without writing) those that do.
	cp := filepath.Join(dir, "closed.json")
	os.WriteFile(cp, []byte("{nope"), 0o644)
	if err := s.Update(func(b *Board) error { b.Agents["a"] = &Agent{Name: "a"}; return nil }); err != nil {
		t.Fatalf("plain update: %v", err)
	}
	if err := s.Update(func(b *Board) error { b.DropClosed("w1"); return nil }); err == nil {
		t.Fatal("update of a broken closed.json succeeded")
	}
	if data, _ := os.ReadFile(cp); string(data) != "{nope" {
		t.Fatalf("closed.json overwritten: %q", data)
	}
}

// The watcher's scrape-only writes are not fsynced; adding an agent and a
// close (board.json and closed.json) are (ADR-113).
func TestOnlyStructuralWritesSync(t *testing.T) {
	syncs := 0
	orig := fileSync
	fileSync = func(f *os.File) error { syncs++; return orig(f) }
	t.Cleanup(func() { fileSync = orig })
	s := NewStore(t.TempDir())
	step := func(name string, want int, fn func(*Board)) {
		t.Helper()
		syncs = 0
		if err := s.Update(func(b *Board) error { fn(b); return nil }); err != nil {
			t.Fatal(err)
		}
		if syncs != want {
			t.Fatalf("%s: %d syncs, want %d", name, syncs, want)
		}
	}
	step("add", 1, func(b *Board) { b.Agents["w1"] = &Agent{Name: "w1"} })
	step("scrape", 0, func(b *Board) { b.Agents["w1"].PaneID = "%2" })
	step("close", 2, func(b *Board) {
		b.AddClosed(b.Agents["w1"], "", time.Now())
		delete(b.Agents, "w1")
	})
}

// A board.json from before ADR-113, or written by an older build still
// running, has its closed records moved to closed.json on the next update,
// the newer of two copies winning.
func TestLegacyClosedMigrates(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	os.WriteFile(filepath.Join(dir, "closed.json"), []byte(`{"x":{"agent":{"name":"x","goal":"old"},"at":"`+now.Add(-time.Hour).Format(time.RFC3339)+`"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "board.json"), []byte(`{"agents":{},"closed":{"x":{"agent":{"name":"x","goal":"new","peer":"p"},"team":"t","at":"`+now.Format(time.RFC3339)+`"},"y":{"agent":{"name":"y"},"at":"`+now.Format(time.RFC3339)+`"}}}`), 0o644)
	s := NewStore(dir)
	if c, ok := s.Load().ClosedRecord("x", now); !ok || c.Agent.Goal != "new" {
		t.Fatalf("read before migration: %+v", c)
	}
	if err := s.Update(func(*Board) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "board.json")); strings.Contains(string(data), "closed") {
		t.Fatalf("legacy key kept: %s", data)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "closed.json"))
	if !strings.Contains(string(data), `"peer": "p"`) || !strings.Contains(string(data), `"y"`) {
		t.Fatalf("closed.json lost records or keys: %s", data)
	}
	if c, _ := s.Load().ClosedRecord("x", now); c == nil || c.Agent.Goal != "new" || c.Team != "t" {
		t.Fatalf("after migration: %+v", c)
	}
}

// An older fleet still running (a resident `fleet watch`, say) must not erase
// board keys a newer one wrote: that is how a leader lost its `leader` flag
// seconds after it was spawned, and with it its squad tools (ADR-58).
func TestUnknownKeysSurviveARoundTrip(t *testing.T) {
	dir := t.TempDir()
	raw := `{"agents":{"a1":{"name":"a1","profile":"fake","status":"idle","leader":true,"from_the_future":{"x":1}}}}`
	if err := os.WriteFile(filepath.Join(dir, "board.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore(dir)
	if err := s.Update(func(b *Board) error { b.Agents["a1"].Status = "working"; return nil }); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "board.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"from_the_future"`) {
		t.Fatalf("unknown key dropped: %s", b)
	}
	if a := s.Load().Agents["a1"]; !a.Leader || a.Status != "working" {
		t.Fatalf("leader=%v status=%q", a.Leader, a.Status)
	}
}

// ADR-58 covered only Agent. A newer fleet's key at the top of the board or
// inside a card, check, report, worktree, exit or baseline must survive an
// older one's write too (ADR-62).
func TestUnknownKeysSurviveAtEveryLevel(t *testing.T) {
	dir := t.TempDir()
	raw := `{"writer":{"v":"9"},"agents":{"a1":{"name":"a1","status":"idle",
	"card":{"goal":"g","k_card":1,"checked":{"at":"2026-01-01T00:00:00Z","cmd":"true","want":0,"code":0,"pass":true,"k_check":1}},
	"report":{"state":"done","summary":"s","at":"2026-01-01T00:00:00Z","k_report":1},
	"worktree":{"path":"/p","branch":"b","k_wt":1},
	"exit":{"at":"2026-01-01T00:00:00Z","k_exit":1},
	"baseline":{"head":"h","k_base":1}}}}`
	os.WriteFile(filepath.Join(dir, "board.json"), []byte(raw), 0o644)
	s := NewStore(dir)
	if err := s.Update(func(b *Board) error { b.Agents["a1"].Status = "working"; return nil }); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "board.json"))
	for _, k := range []string{"writer", "k_card", "k_check", "k_report", "k_wt", "k_exit", "k_base"} {
		if !strings.Contains(string(b), `"`+k+`"`) {
			t.Errorf("%s dropped: %s", k, b)
		}
	}
}

// An Update that changes nothing leaves board.json alone: the watcher
// refreshes every second, and an idle fleet rewrote the file each time
// (ADR-62).
func TestNoOpUpdateDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	s.Update(func(b *Board) error { b.Agents["a1"] = &Agent{Name: "a1", Status: "idle"}; return nil })
	before, err := os.Stat(filepath.Join(dir, "board.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(b *Board) error { b.Agents["a1"].Status = "idle"; return nil }); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(filepath.Join(dir, "board.json"))
	if !os.SameFile(before, after) {
		t.Fatal("a no-op update replaced board.json")
	}
	s.Update(func(b *Board) error { b.Agents["a1"].Status = "working"; return nil })
	if s.Load().Agents["a1"].Status != "working" {
		t.Fatal("a real change was not written")
	}
}

func TestNoReport(t *testing.T) {
	now := time.Now()
	task := now.Add(-time.Hour)
	idle := func(mod func(*Agent)) *Agent {
		a := &Agent{Status: "idle", StatusSince: now.Add(-time.Minute), PromptAt: task}
		if mod != nil {
			mod(a)
		}
		return a
	}
	for _, c := range []struct {
		name string
		a    *Agent
		want bool
	}{
		{"idle after a task, no report", idle(nil), true},
		{"progress is not an ending", idle(func(a *Agent) { a.Report = &Report{State: "progress", At: now} }), true},
		{"done before this task", idle(func(a *Agent) { a.Report = &Report{State: "done", At: task.Add(-time.Second)} }), true},
		{"done", idle(func(a *Agent) { a.Report = &Report{State: "done", At: now} }), false},
		{"question", idle(func(a *Agent) { a.Report = &Report{State: "question", At: now} }), false},
		{"never given a task (adopted)", idle(func(a *Agent) { a.PromptAt = time.Time{} }), false},
		{"working", idle(func(a *Agent) { a.Status = "working" }), false},
		{"parked", idle(func(a *Agent) { a.Status = "parked" }), false},
		{"between turns", idle(func(a *Agent) { a.StatusSince = now.Add(-time.Second) }), false},
		{"screen still changing", idle(func(a *Agent) { a.LastChange = now.Add(-5 * time.Second) }), false},
		{"screen still for the window", idle(func(a *Agent) { a.LastChange = now.Add(-NoReportAfter) }), true},
		{"told this idle spell, then the footer ticked", idle(func(a *Agent) {
			a.NoReportTold, a.LastChange = now.Add(-30*time.Second), now.Add(-time.Second)
		}), true},
		{"told before this idle spell, screen changing", idle(func(a *Agent) {
			a.NoReportTold, a.LastChange = now.Add(-2*time.Minute), now.Add(-time.Second)
		}), false},
		{"told for an earlier task, screen changing", idle(func(a *Agent) {
			a.NoReportTold, a.LastChange = task.Add(-time.Second), now.Add(-time.Second)
		}), false},
		{"told but working again", idle(func(a *Agent) { a.Status, a.NoReportTold = "working", now.Add(-30*time.Second) }), false},
		{"interactive", idle(func(a *Agent) { a.Interactive = true }), false},
		{"user typed lately", idle(func(a *Agent) { a.UserAt = now.Add(-time.Minute) }), false},
	} {
		if got := c.a.NoReport(now); got != c.want {
			t.Errorf("%s: NoReport = %v, want %v", c.name, got, c.want)
		}
	}
}

// The user's agents get the soft flag: a turn that ended with no report of
// any kind since it began, while the user is not typing (ADR-68 amendment).
func TestUserNoReport(t *testing.T) {
	now := time.Now()
	turn := now.Add(-10 * time.Minute)
	idle := func(mod func(*Agent)) *Agent {
		// An agent the user typed in on 2026-11-09: a progress report from
		// an earlier turn, then "Logging everything now." and idle.
		a := &Agent{Status: "idle", StatusSince: now.Add(-time.Minute), TurnAt: turn,
			UserAt: now.Add(-2 * time.Minute), Report: &Report{State: "progress", At: turn.Add(-time.Hour)}}
		if mod != nil {
			mod(a)
		}
		return a
	}
	for _, c := range []struct {
		name      string
		a         *Agent
		user, tag string
	}{
		{"typed lately, turn ended without a report", idle(nil), "user", "user"},
		{"interactive, turn ended without a report", idle(func(a *Agent) { a.Interactive, a.UserAt = true, time.Time{} }), "user", "user"},
		{"progress in this turn ends it", idle(func(a *Agent) { a.Report = &Report{State: "progress", At: turn.Add(time.Minute)} }), "", ""},
		{"no report ever", idle(func(a *Agent) { a.Report = nil }), "user", "user"},
		{"the user is typing", idle(func(a *Agent) { a.UserAt = now.Add(-5 * time.Second) }), "", ""},
		{"between turns", idle(func(a *Agent) { a.StatusSince = now.Add(-time.Second) }), "", ""},
		{"screen still changing", idle(func(a *Agent) { a.LastChange = now.Add(-5 * time.Second) }), "", ""},
		{"working", idle(func(a *Agent) { a.Status = "working" }), "", ""},
		{"no turn seen", idle(func(a *Agent) { a.TurnAt = time.Time{} }), "", ""},
		{"not the user's any more: the hard flag", idle(func(a *Agent) { a.UserAt, a.PromptAt = now.Add(-time.Hour), turn }), "", "1"},
		{"not the user's, never handed work", idle(func(a *Agent) { a.UserAt = now.Add(-time.Hour) }), "", ""},
	} {
		got := ""
		if c.a.UserNoReport(now) {
			got = "user"
		}
		if got != c.user {
			t.Errorf("%s: UserNoReport = %q, want %q", c.name, got, c.user)
		}
		if tag := c.a.NoReportTag(now); tag != c.tag {
			t.Errorf("%s: NoReportTag = %q, want %q", c.name, tag, c.tag)
		}
	}
}

// A question or blocked report waits for an answer until the agent's next
// turn starts, and counts as answered from then on (ADR-90, fleet#10, #14).
func TestAskingAndAnswered(t *testing.T) {
	now := time.Now()
	asked := now.Add(-time.Hour)
	agent := func(state string, turnAt time.Time, st string) *Agent {
		return &Agent{Status: st, TurnAt: turnAt, Report: &Report{State: state, At: asked}}
	}
	for _, c := range []struct {
		name            string
		a               *Agent
		asking, answers bool
		shown           string
	}{
		{"question, idle since", agent("question", asked.Add(-time.Minute), "idle"), true, false, "waiting"},
		{"question, no turn ever seen", agent("question", time.Time{}, "idle"), true, false, "waiting"},
		{"question, answered by a send queued mid-turn", func() *Agent {
			a := agent("question", asked.Add(-time.Minute), "idle")
			a.PromptAt = asked.Add(time.Second)
			return a
		}(), false, false, "idle"},
		{"question, still in its turn", agent("question", asked.Add(-time.Minute), "working"), false, false, "working"},
		{"question, a menu on screen", agent("question", asked.Add(-time.Minute), "waiting"), false, false, "waiting"},
		{"question, next turn started", agent("question", asked.Add(time.Minute), "working"), false, true, "working"},
		{"question, next turn ended", agent("question", asked.Add(time.Minute), "idle"), false, true, "idle"},
		{"blocked, next turn started", agent("blocked", asked.Add(time.Minute), "working"), false, true, "working"},
		{"blocked, idle since", agent("blocked", asked.Add(-time.Minute), "idle"), false, false, "idle"},
		{"progress is never answered", agent("progress", asked.Add(time.Minute), "idle"), false, false, "idle"},
		{"no report", &Agent{Status: "idle", TurnAt: asked}, false, false, "idle"},
	} {
		if got := c.a.Asking(); got != c.asking {
			t.Errorf("%s: Asking = %v, want %v", c.name, got, c.asking)
		}
		if got := c.a.Answered(); got != c.answers {
			t.Errorf("%s: Answered = %v, want %v", c.name, got, c.answers)
		}
		if got := c.a.Shown(); got != c.shown {
			t.Errorf("%s: Shown = %q, want %q", c.name, got, c.shown)
		}
	}
}

func TestDriverName(t *testing.T) {
	if got := (&Agent{}).DriverName(); got != "tmux" {
		t.Errorf("no driver field (pre-v1.0.0 agent): %q, want tmux", got)
	}
	if got := (&Agent{Driver: DriverACP}).DriverName(); got != "acp" {
		t.Errorf("acp agent: %q", got)
	}
}

func TestPruneClosed(t *testing.T) {
	now := time.Now()
	b := &Board{}
	b.AddClosed(&Agent{Name: "old"}, "", now.Add(-ClosedKeep-time.Minute))
	for i := 0; i < ClosedMax+1; i++ {
		b.AddClosed(&Agent{Name: fmt.Sprintf("a%02d", i), PaneID: "%1"}, "t", now.Add(time.Duration(i)*time.Second))
	}
	set := b.closedSet()
	if len(set) != ClosedMax || set["old"] != nil || set["a00"] != nil || set["a50"] == nil {
		t.Fatalf("kept %d: old=%v a00=%v", len(set), set["old"], set["a00"])
	}
	if set["a50"].Agent.PaneID != "" || set["a50"].Team != "t" {
		t.Errorf("record %+v", set["a50"])
	}
}
