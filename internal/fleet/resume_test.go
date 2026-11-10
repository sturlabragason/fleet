package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/supervisor"
)

// resumeFleet is adoptFleet with config.toml set to cfg, and agent "w1"
// adopted on session S1 in its own dir, given a card, a team and owner adm.
func resumeFleet(t *testing.T, cfg, adm string) (*Fleet, string) {
	t.Helper()
	f, sess := adoptFleet(t)
	p := filepath.Join(f.Home, "config.toml")
	os.WriteFile(p, []byte(cfg), 0o644)
	t.Setenv("FLEET_CONFIG", p)
	var err error
	if f.Prefs, err = prefs.Load(); err != nil {
		t.Fatal(err)
	}
	f.FreeMem = func() (uint64, error) { return 64 << 30, nil }
	if adm != "" {
		if _, err := supervisor.Add(f.Home, adm, "", 3); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Join(sess, "SESSION1"), 0o755)
	dir := t.TempDir()
	if _, err := f.Adopt(AdoptOpts{Profile: "rs", SessionID: "SESSION1", Dir: dir, Name: "w1", Admiral: adm}); err != nil {
		t.Fatal(err)
	}
	f.Store.Update(func(b *board.Board) error {
		b.Agents["w1"].Card = &board.Card{Goal: "fix it", Result: "result.md"}
		b.Agents["w1"].Goal = "the original task"
		return nil
	})
	if _, err := f.Teams.Add("red", "w1"); err != nil {
		t.Fatal(err)
	}
	return f, dir
}

func TestResumeClosedAgent(t *testing.T) {
	f, dir := resumeFleet(t, "[defaults]\nready_timeout = \"100ms\"\n", "print")
	if err := f.Close("w1"); err != nil {
		t.Fatal(err)
	}
	b := f.Store.Load()
	if _, ok := b.Agents["w1"]; ok || b.Closed["w1"] == nil {
		t.Fatalf("after close: agents %v closed %v", b.Agents, b.Closed)
	}
	err := f.ClosedError("w1", "pass resume=true")
	if err == nil || !strings.Contains(err.Error(), "w1 closed at ") || !strings.HasSuffix(err.Error(), "pass resume=true to resume its last session") {
		t.Fatalf("refusal: %v", err)
	}
	if err := f.CheckScope("", "w1"); err == nil {
		t.Error("main may resume print's closed agent")
	}
	if err := f.CheckScope("print", "w1"); err != nil {
		t.Errorf("owner refused: %v", err)
	}

	a, held, err := f.Resume("w1", "carry on")
	if err != nil || held {
		t.Fatalf("resume: %v held=%v", err, held)
	}
	if a.SessionID != "SESSION1" || a.Dir != dir || a.Card == nil || a.Card.Goal != "fix it" || a.Owner() != "print" || a.Profile != "rs" {
		t.Errorf("resumed %+v", a)
	}
	if team, _ := f.Teams.Of("w1"); team != "red" {
		t.Errorf("team %q", team)
	}
	b = f.Store.Load()
	if b.Closed["w1"] != nil || b.Agents["w1"] == nil || b.Agents["w1"].PromptAt.IsZero() {
		t.Errorf("board after resume: closed %v agent %+v", b.Closed["w1"], b.Agents["w1"])
	}
	if f.ClosedError("w1", "x") != nil {
		t.Error("a running agent is refused as closed")
	}
	var screen string
	for i := 0; i < 30 && !strings.Contains(screen, "carry on"); i++ {
		time.Sleep(100 * time.Millisecond)
		screen, _ = f.Peek("w1", 0)
	}
	if !strings.Contains(screen, "carry on") {
		t.Errorf("text not delivered: %q", screen)
	}
}

func TestResumeRefusals(t *testing.T) {
	f, _ := resumeFleet(t, "", "")
	if _, _, err := f.Resume("nobody", "x"); err == nil || !strings.Contains(err.Error(), "no record") {
		t.Errorf("unknown: %v", err)
	}
	// A worktree removed after the close is never re-created.
	wt := t.TempDir()
	f.Store.Update(func(b *board.Board) error {
		b.Agents["w1"].Worktree = &board.Worktree{Path: wt, Branch: "fleet/w1"}
		return nil
	})
	if err := f.Close("w1"); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(wt)
	if _, _, err := f.Resume("w1", "x"); err == nil || !strings.Contains(err.Error(), "worktree") {
		t.Errorf("gone worktree: %v", err)
	}
	if _, err := os.Stat(wt); err == nil {
		t.Error("worktree re-created")
	}
	// A new spawn of the name drops the record.
	if _, err := f.Spawn("w1", "rs", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	if f.Store.Load().Closed["w1"] != nil {
		t.Error("record kept after a new spawn of the name")
	}
	// A record older than ClosedKeep is neither named nor resumed.
	f.Store.Update(func(b *board.Board) error {
		b.Closed = map[string]*board.Closed{"old": {Agent: board.Agent{Name: "old", SessionID: "SESSION1", Dir: t.TempDir()}, At: time.Now().Add(-board.ClosedKeep - time.Minute)}}
		return nil
	})
	if err := f.ClosedError("old", "x"); err != nil {
		t.Errorf("expired record refused a send: %v", err)
	}
	if _, _, err := f.Resume("old", "x"); err == nil || !strings.Contains(err.Error(), "no record") {
		t.Errorf("expired record resumed: %v", err)
	}
}

func TestResumeQueuesOverCap(t *testing.T) {
	f, _ := resumeFleet(t, "[defaults]\nmax_working = 1\nready_timeout = \"100ms\"\n", "")
	if err := f.Close("w1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Spawn("busy", "rs", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	f.Store.Update(func(b *board.Board) error { b.Agents["busy"].Status = "working"; return nil })
	a, held, err := f.Resume("w1", "first")
	if err != nil || !held || a.Status != "queued" || a.SessionID != "SESSION1" || a.Card == nil {
		t.Fatalf("resume over cap: %v held=%v %+v", err, held, a)
	}
	if h, _ := f.HoldForQueued("w1", "second"); !h {
		t.Fatal("send to a queued resume not held")
	}
	f.Refresh() // classify busy's pane once, so the idle below sticks
	f.Store.Update(func(b *board.Board) error { b.Agents["busy"].Status = "idle"; return nil })
	f.DrainQueue, f.DrainSync = true, true
	f.Refresh()
	got := f.Store.Load().Agents["w1"]
	if got == nil || got.PaneID == "" || got.SessionID != "SESSION1" || got.Card == nil || got.Card.Goal != "fix it" || got.Goal != "the original task" {
		t.Fatalf("drained resume %+v", got)
	}
	var screen string
	for i := 0; i < 30 && !strings.Contains(screen, "second"); i++ {
		time.Sleep(100 * time.Millisecond)
		screen, _ = f.Peek("w1", 0)
	}
	if !strings.Contains(screen, "first") || !strings.Contains(screen, "second") {
		t.Errorf("held text not delivered: %q", screen)
	}
}
