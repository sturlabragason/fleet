package wall

import (
	"testing"
)

// u swaps the last replace back into the same slot and a second u redoes it.
func TestUndoSwapsBackAndRedo(t *testing.T) {
	c, w, agents := setupWall(t, 5)
	fillWall(t, w, agents)
	x := agents[1]
	w.setOpt("@fleet_target", x)
	before := slots(t, c)
	if ok, err := w.Show(agents[4]); err != nil || !ok {
		t.Fatalf("show: %v %v", ok, err)
	}
	msg, back, err := w.Undo()
	if err != nil {
		t.Fatal(err)
	}
	if back != "a1" || msg != "a1 back in ▶ (was a4) · u redo" {
		t.Errorf("undo: %q %q", msg, back)
	}
	if got := slots(t, c); slotIndex(got, x) != slotIndex(before, x) || len(got) != len(before) {
		t.Errorf("a1 not back in its slot: %v -> %v", before, got)
	}
	if on := w.OnWall(); !on["a1"] || on["a4"] {
		t.Errorf("on wall after undo: %v", on)
	}
	if got := w.opt("@fleet_target"); got != x {
		t.Errorf("target %q, want %s", got, x)
	}
	if got := w.opt("@fleet_undo"); got != x+" "+agents[4]+" a4" {
		t.Errorf("reverse not recorded: %q", got)
	}
	msg, back, err = w.Undo()
	if err != nil || back != "a4" || msg != "a4 back in ▶ (was a1) · u redo" {
		t.Errorf("redo: %q %q %v", msg, back, err)
	}
	if on := w.OnWall(); on["a1"] || !on["a4"] {
		t.Errorf("on wall after redo: %v", on)
	}
}

// A record whose panes moved on since (the incoming pane left the wall, or
// the replaced pane came back) is not undone, and is cleared.
func TestUndoWithAStaleRecord(t *testing.T) {
	_, w, agents := setupWall(t, 5)
	fillWall(t, w, agents)
	w.setOpt("@fleet_target", agents[1])
	if ok, err := w.Show(agents[4]); err != nil || !ok {
		t.Fatalf("show: %v %v", ok, err)
	}
	if err := w.Hide(agents[4]); err != nil {
		t.Fatal(err)
	}
	msg, back, err := w.Undo()
	if err != nil || back != "" || msg != NothingToUndoMsg {
		t.Errorf("stale undo: %q %q %v", msg, back, err)
	}
	if got := w.opt("@fleet_undo"); got != "" {
		t.Errorf("stale record kept: %q", got)
	}
	if msg, _, _ := w.Undo(); msg != NothingToUndoMsg {
		t.Errorf("empty record: %q", msg)
	}
}

// The pane that left is dead: say so by name.
func TestUndoWhenThePaneIsGone(t *testing.T) {
	c, w, agents := setupWall(t, 5)
	fillWall(t, w, agents)
	w.setOpt("@fleet_target", agents[1])
	if ok, err := w.Show(agents[4]); err != nil || !ok {
		t.Fatalf("show: %v %v", ok, err)
	}
	c.Run("set-option", "-p", "-t", agents[1], "remain-on-exit", "on")
	c.Run("respawn-pane", "-k", "-t", agents[1], "true")
	for i := 0; i < 50; i++ {
		if d, _ := c.Run("display-message", "-p", "-t", agents[1], "#{pane_dead}"); d == "1" {
			break
		}
		c.Run("run-shell", "sleep 0.02")
	}
	msg, back, _ := w.Undo()
	if back != "" || msg != "nothing to undo (a1 is gone)" {
		t.Errorf("gone: %q %q", msg, back)
	}
}

// The pane that left was killed outright: the recorded name still says who.
func TestUndoWhenThePaneWasKilled(t *testing.T) {
	c, w, agents := setupWall(t, 5)
	fillWall(t, w, agents)
	w.setOpt("@fleet_target", agents[1])
	if ok, err := w.Show(agents[4]); err != nil || !ok {
		t.Fatalf("show: %v %v", ok, err)
	}
	if _, err := c.Run("kill-pane", "-t", agents[1]); err != nil {
		t.Fatal(err)
	}
	msg, back, err := w.Undo()
	if err != nil || back != "" || msg != "nothing to undo (a1 is gone)" {
		t.Errorf("killed: %q %q %v", msg, back, err)
	}
	if got := w.opt("@fleet_undo"); got != "" {
		t.Errorf("record kept: %q", got)
	}
}

// A two-field record from before the name was kept still undoes.
func TestUndoReadsAnOldRecord(t *testing.T) {
	_, w, agents := setupWall(t, 5)
	fillWall(t, w, agents)
	w.setOpt("@fleet_target", agents[1])
	if ok, err := w.Show(agents[4]); err != nil || !ok {
		t.Fatalf("show: %v %v", ok, err)
	}
	w.setOpt("@fleet_undo", agents[4]+" "+agents[1])
	if msg, back, err := w.Undo(); err != nil || back != "a1" || msg != "a1 back in ▶ (was a4) · u redo" {
		t.Errorf("old record: %q %q %v", msg, back, err)
	}
	if in, out, name := parseUndo("%1 %2"); in != "%1" || out != "%2" || name != "" {
		t.Errorf("parseUndo two fields: %q %q %q", in, out, name)
	}
	if in, _, _ := parseUndo("%1"); in != "" {
		t.Errorf("parseUndo one field: %q", in)
	}
}

func TestShownMsg(t *testing.T) {
	for _, c := range []struct {
		before          string
		wasOn, beforeOn bool
		want            string
	}{
		{"", true, false, "b on wall"},
		{"a", false, false, "b → ▶ (was a) · u undo"},
		{"a", false, true, "a is pinned · p unpins"},
		{"", false, false, "b added to wall"},
	} {
		if got := ShownMsg("b", c.before, c.wasOn, c.beforeOn); got != c.want {
			t.Errorf("%+v: %q", c, got)
		}
	}
}

// p toggles @fleet_pin, on or off the wall, and a pinned target is not
// replaced.
func TestTogglePin(t *testing.T) {
	c, w, agents := setupWall(t, 5)
	fillWall(t, w, agents)
	x := agents[1]
	if on, err := w.TogglePin(x); err != nil || !on {
		t.Fatalf("pin: %v %v", on, err)
	}
	if v, _ := c.Run("show-options", "-pqv", "-t", x, "@fleet_pin"); v != "1" {
		t.Errorf("@fleet_pin %q", v)
	}
	if p := w.Pinned(); !p["a1"] || len(p) != 1 {
		t.Errorf("Pinned %v", p)
	}
	w.setOpt("@fleet_target", x)
	if _, err := w.Show(agents[4]); err != nil {
		t.Fatal(err)
	}
	if on := w.OnWall(); !on["a1"] {
		t.Errorf("pinned target replaced: %v", on)
	}
	if on, err := w.TogglePin(x); err != nil || on {
		t.Fatalf("unpin: %v %v", on, err)
	}
	if v, _ := c.Run("show-options", "-pqv", "-t", x, "@fleet_pin"); v != "" {
		t.Errorf("@fleet_pin after unpin %q", v)
	}
	// off the wall too
	if on, err := w.TogglePin(agents[0]); err != nil || !on {
		t.Errorf("pin off-wall pane: %v %v", on, err)
	}
}
