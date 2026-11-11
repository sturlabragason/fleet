package fleet

import (
	"os"
	"reflect"
	"testing"

	"github.com/sturlabragason/fleet/internal/board"
)

// An ACP agent whose host runs another binary than f.Exe is stale; a tmux
// agent never is, and a parked one has no host to restart.
func TestStaleHosts(t *testing.T) {
	f, _ := parkFleet(t, "")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f.Exe = exe
	pane, err := f.T.Run("new-session", "-d", "-s", Session, "-P", "-F", "#{pane_id}", "exec sleep 600")
	if err != nil {
		t.Fatal(err)
	}
	f.Store.Update(func(b *board.Board) error {
		b.Agents["host"] = &board.Agent{Name: "host", Driver: board.DriverACP, PaneID: pane}
		b.Agents["parked"] = &board.Agent{Name: "parked", Driver: board.DriverACP}
		b.Agents["screen"] = &board.Agent{Name: "screen", PaneID: pane}
		return nil
	})
	if got := f.StaleHosts(); !reflect.DeepEqual(got, []string{"host"}) {
		t.Fatalf("stale %v", got)
	}
}
