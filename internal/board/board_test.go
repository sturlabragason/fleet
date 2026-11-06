package board

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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
