package tmux

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"
)

// One list-panes -a carries the panes and their window's options, an unset
// last option included, and survives the file round trip (ADR-98).
func TestSnapshotPanesAndWindowOptions(t *testing.T) {
	c := &Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { c.KillServer() })
	sup, err := c.Run("new-session", "-d", "-s", "f", "-n", "main", "-x", "200", "-y", "50", "-P", "-F", "#{pane_id}", "cat")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	c.Run("split-window", "-d", "-t", "f:main", "cat")
	ag, _ := c.Spawn("f", "ag-a1", "", nil, "cat")
	c.SetPaneOption(ag, "@fleet_agent", "a1")
	c.SetPaneOption(ag, "@fleet_pin", "1")
	c.Run("set-option", "-w", "-t", "f:main", "@fleet_wall", "6")
	c.Run("set-option", "-w", "-t", "f:main", "@fleet_target", sup)

	execs := 0
	c.OnRun = func([]string) { execs++ }
	s, err := c.Snapshot()
	c.OnRun = nil
	if err != nil {
		t.Fatal(err)
	}
	if execs != 1 {
		t.Errorf("snapshot ran %d tmux commands, want 1", execs)
	}
	path := filepath.Join(t.TempDir(), "tmux.json")
	if err := WriteSnapshot(path, s); err != nil {
		t.Fatal(err)
	}
	if s, err = ReadSnapshot(path); err != nil {
		t.Fatal(err)
	}
	if len(s.Panes) != 3 || len(s.PanesOf("f:main")) != 2 || len(s.PanesOf("f:")) != 3 {
		t.Fatalf("panes %d, main %d", len(s.Panes), len(s.PanesOf("f:main")))
	}
	win, ok := s.Window("f:main")
	if !ok || win["@fleet_wall"] != "6" || win["@fleet_target"] != sup || win["window_width"] != "200" {
		t.Fatalf("main window %v", win)
	}
	if v, ok := win["@fleet_sup_seen"]; !ok || v != "" {
		t.Errorf("unset last option: %q %v", v, ok)
	}
	if p := s.PanesOf("f:ag-a1"); len(p) != 1 || p[0].Agent != "a1" || !p[0].Pin {
		t.Errorf("agent pane %+v", p)
	}
}

// A grouped session (the two-monitor wall session, ADR-108) shares every
// window, and list-panes -a lists each pane once per session: ListPanes and
// Snapshot keep one row per pane, the first session's.
func TestGroupedSessionListsEachPaneOnce(t *testing.T) {
	c := &Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { c.KillServer() })
	if _, err := c.Run("new-session", "-d", "-s", "f", "-n", "main", "cat"); err != nil {
		t.Skip("tmux unavailable")
	}
	c.Spawn("f", "ag-a1", "", nil, "cat")
	if _, err := c.Run("new-session", "-d", "-t", "=f", "-s", "f-wall"); err != nil {
		t.Fatal(err)
	}
	ps, err := c.ListPanes("")
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string][]PaneInfo{"ListPanes": ps, "Snapshot": s.Panes} {
		if len(got) != 2 {
			t.Fatalf("%s: %d panes, want 2: %+v", name, len(got), got)
		}
		for _, p := range got {
			if p.Session != "f" {
				t.Errorf("%s: pane %s from session %q, want f", name, p.ID, p.Session)
			}
		}
	}
}
