package fleet

import (
	"fmt"
	"maps"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/tmux"
)

// With ShareSnapshot set, Refresh lists the panes once, through the
// snapshot, and the snapshot it hands over carries the status tags this
// refresh wrote (ADR-98).
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

// An idle pass costs no per-pane or per-option tmux call: a pane tmux wrote
// nothing to since the last capture is not captured again, and the badge and
// admiral-border options are compared against the snapshot. Output to the
// pane brings its capture back (ADR-138).
func TestIdleRefreshSkipsCaptureAndOptionReads(t *testing.T) {
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
	a, err := f.Spawn("a1", "quiet", home, "")
	if err != nil {
		t.Fatal(err)
	}
	pane := a.PaneID
	gen := func() string {
		ps, _ := tc.ListPanes("")
		for _, p := range ps {
			if p.ID == pane {
				return p.OutputGen
			}
		}
		return ""
	}
	if gen() == "" {
		t.Skip("tmux has no pane_output_generation")
	}
	f.ShareSnapshot = func(*tmux.Snapshot) {}
	counts := map[string]int{}
	tc.OnRun = func(args []string) { counts[args[0]]++ }
	pass := func() map[string]int {
		t.Helper()
		clear(counts)
		if _, err := f.Refresh(); err != nil {
			t.Fatal(err)
		}
		snap, err := tc.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		f.syncBadge(snap)
		return maps.Clone(counts)
	}
	pass() // the first pass captures and sets what is missing
	if got := pass(); got["capture-pane"] != 0 || got["show-options"] != 0 {
		t.Fatalf("idle pass ran %v, want no capture-pane or show-options", got)
	}
	before := gen()
	tc.Run("send-keys", "-t", pane, "x")
	deadline := time.Now().Add(5 * time.Second)
	for gen() == before && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if got := pass(); got["capture-pane"] != 1 {
		t.Fatalf("pass after output ran %v, want one capture-pane", got)
	}
}
