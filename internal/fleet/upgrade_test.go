package fleet

import (
	"testing"

	"github.com/sturlabragason/fleet/internal/board"
)

func TestUpgradeQuietWaitsForWorkingAgents(t *testing.T) {
	home := t.TempDir()
	f := &Fleet{Home: home, Store: board.NewStore(home)}
	set := func(st string) {
		f.Store.Update(func(b *board.Board) error {
			b.Agents["a1"] = &board.Agent{Name: "a1", Status: st}
			return nil
		})
	}
	set("working")
	if f.UpgradeQuiet() {
		t.Fatal("quiet while an agent works")
	}
	set("idle")
	if !f.UpgradeQuiet() {
		t.Fatal("not quiet with every agent idle")
	}
}
