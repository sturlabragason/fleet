package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/sturlabragason/fleet/internal/tmux"
)

// With ShareSnapshot set, Refresh lists the panes once, through the
// snapshot, and the snapshot it hands over carries the status tags this
// refresh wrote (ADR-94).
func TestRefreshSharesItsSnapshot(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "config.toml")
	os.WriteFile(cfg, []byte("[defaults]\nplaybook = false\nworktree = false\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[quiet]
command = "sh"
args = ["-c", "printf '> \n'; sleep 600"]
patterns = "fake"
`), 0o644)
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Spawn("a1", "quiet", home, ""); err != nil {
		t.Fatal(err)
	}
	var got *tmux.Snapshot
	f.ShareSnapshot = func(s *tmux.Snapshot) { got = s }
	lists := 0
	tc.OnRun = func(args []string) {
		if args[0] == "list-panes" {
			lists++
		}
	}
	as, err := f.Refresh()
	tc.OnRun = nil
	if err != nil {
		t.Fatal(err)
	}
	if lists != 1 || got == nil {
		t.Fatalf("list-panes %d times, snapshot %v", lists, got != nil)
	}
	live, _ := tc.ListPanes("")
	var tag, snapTag string
	for _, p := range live {
		if p.Agent == "a1" {
			tag = p.Status
		}
	}
	for _, p := range got.Panes {
		if p.Agent == "a1" {
			snapTag = p.Status
		}
	}
	if tag == "" || snapTag != tag || len(as) != 1 || as[0].Shown() != tag {
		t.Errorf("pane tag %q, snapshot %q, board %+v", tag, snapTag, as)
	}
}
