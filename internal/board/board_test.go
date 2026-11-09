package board

import (
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
