package wall

import (
	"strings"
	"testing"
)

func pids(t *testing.T, w *Wall, panes []string) []string {
	t.Helper()
	var out []string
	for _, p := range panes {
		v, err := w.T.Run("display-message", "-p", "-t", p, "#{pane_pid} #{pane_dead}")
		if err != nil {
			t.Fatalf("pane %s gone: %v", p, err)
		}
		out = append(out, strings.TrimSpace(v))
	}
	return out
}

func TestHideAllKeepsAgentsRunningAndRestores(t *testing.T) {
	_, w, agents := setupWall(t, 5)
	fillWall(t, w, agents)
	if _, err := w.TogglePin(agents[1]); err != nil {
		t.Fatal(err)
	}
	before := pids(t, w, agents)
	n, err := w.HideAll()
	if err != nil || n != 4 {
		t.Fatalf("HideAll = %d, %v; want 4", n, err)
	}
	s, _ := w.Read()
	if len(s.Wall) != 0 || len(s.Sups) == 0 || s.Ov == "" {
		t.Fatalf("after HideAll: wall %d, sups %v, ov %q; want empty wall, admiral and overview kept", len(s.Wall), s.Sups, s.Ov)
	}
	if got := pids(t, w, agents); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Errorf("agent processes changed: %v -> %v", before, got)
	}
	if w.opt("@fleet_target") != "" {
		t.Error("the target must go with the panes")
	}
	n, err = w.Restore()
	if err != nil || n != 4 {
		t.Fatalf("Restore = %d, %v; want 4", n, err)
	}
	s, _ = w.Read()
	var ids []string
	for _, p := range s.Wall {
		ids = append(ids, p.ID)
	}
	if strings.Join(ids, " ") != strings.Join(agents[:4], " ") {
		t.Errorf("restored %v, want %v in order", ids, agents[:4])
	}
	if n, _ := w.Restore(); n != 0 {
		t.Errorf("second Restore brought back %d, want 0", n)
	}
}

func TestHideAllOnAnEmptyWallIsANoOp(t *testing.T) {
	_, w, agents := setupWall(t, 2)
	n, err := w.HideAll()
	if err != nil || n != 0 {
		t.Fatalf("HideAll on empty wall = %d, %v", n, err)
	}
	if msg, err := w.ToggleAll(); err != nil || msg != NothingOnWallMsg {
		t.Errorf("ToggleAll on empty wall = %q, %v", msg, err)
	}
	// An empty wall keeps the last record, so Restore still works after a
	// second hide-all.
	noTarget(t, w)
	w.Show(agents[0])
	if n, _ := w.HideAll(); n != 1 {
		t.Fatalf("HideAll = %d", n)
	}
	if n, _ := w.HideAll(); n != 0 {
		t.Fatal("second HideAll must do nothing")
	}
	if msg, err := w.ToggleAll(); err != nil || msg != RestoredMsg(1) {
		t.Errorf("ToggleAll = %q, %v; want restore", msg, err)
	}
	if !w.OnWall()["a0"] {
		t.Error("a0 not restored")
	}
	if msg, _ := w.ToggleAll(); msg != HiddenAllMsg(1) {
		t.Errorf("ToggleAll on a full wall = %q, want hide all", msg)
	}
}

func TestRestoreSkipsGonePanes(t *testing.T) {
	c, w, agents := setupWall(t, 3)
	for _, p := range agents {
		noTarget(t, w)
		w.Show(p)
	}
	if n, _ := w.HideAll(); n != 3 {
		t.Fatalf("HideAll = %d", n)
	}
	c.KillPane(agents[1])
	n, err := w.Restore()
	if err != nil || n != 2 {
		t.Fatalf("Restore = %d, %v; want 2", n, err)
	}
	if on := w.OnWall(); !on["a0"] || on["a1"] || !on["a2"] {
		t.Errorf("on wall %v", on)
	}
}
