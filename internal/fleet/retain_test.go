package fleet

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/board"
)

// Only old files of names fleet no longer knows go: an agent's files, and a
// closed record's (it can still be resumed), stay whatever their age.
func TestPruneACPKeepsKnownNames(t *testing.T) {
	home := t.TempDir()
	f := &Fleet{Home: home, Store: board.NewStore(home)}
	now := time.Now()
	f.Store.Update(func(b *board.Board) error {
		b.Agents["live"] = &board.Agent{Name: "live"}
		b.AddClosed(&board.Agent{Name: "shut"}, "", now)
		return nil
	})
	dir := acphost.Dir(home)
	os.MkdirAll(dir, 0o700)
	old := now.Add(-ACPKeep - time.Hour)
	files := map[string]bool{ // name -> kept
		"live.jsonl": true, "live.stderr": true, "shut.jsonl": true, "shut.prompt": true,
		"gone.jsonl": false, "gone.stderr": false, "gone.sock": false, "new.jsonl": true, "notes.txt": true,
	}
	for n := range files {
		p := filepath.Join(dir, n)
		os.WriteFile(p, []byte("x"), 0o600)
		if n != "new.jsonl" {
			os.Chtimes(p, old, old)
		}
	}
	if got := f.pruneACP(now); got != 3 {
		t.Errorf("removed %d files, want 3", got)
	}
	for n, kept := range files {
		if _, err := os.Stat(filepath.Join(dir, n)); (err == nil) != kept {
			t.Errorf("%s: kept=%v, want %v", n, err == nil, kept)
		}
	}
}

// A board.json or closed.json that cannot be read is not "no agents": the
// pass deletes nothing until it is repaired.
func TestPruneACPSkipsBrokenBoard(t *testing.T) {
	for _, broken := range []string{"board.json", "closed.json"} {
		home := t.TempDir()
		f := &Fleet{Home: home, Store: board.NewStore(home)}
		os.WriteFile(filepath.Join(home, broken), []byte("{not json"), 0o600)
		dir := acphost.Dir(home)
		os.MkdirAll(dir, 0o700)
		p := filepath.Join(dir, "parked.prompt")
		os.WriteFile(p, []byte("x"), 0o600)
		old := time.Now().Add(-ACPKeep - time.Hour)
		os.Chtimes(p, old, old)
		if got := f.pruneACP(time.Now()); got != 0 {
			t.Errorf("%s broken: removed %d", broken, got)
		}
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s broken: %v", broken, err)
		}
	}
}
