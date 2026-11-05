package wall

import (
	"strconv"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/tmux"
)

// Window sizes used here, in cells, and the wall they leave (DESIGN §5b.4):
//
//	laptop  160x42 -> 95x27 wall,  floor 40x10, 4 panes
//	1080p   210x52 -> 145x37 wall, floor 40x10, 9 panes
//	split   120x35 -> 55x20 wall,  floor 40x10, 1 pane
const (
	laptopW, laptopH = 160, 42
	splitW, splitH   = 120, 35
)

func TestSlotsStepDownWithTheWindow(t *testing.T) {
	cases := []struct {
		name       string
		w, h       int
		preset     string
		wantSlots  int
		wantCapped bool
	}{
		{"reference, 9 fits", 300, 80, "9", 9, false},
		{"1080p, 9 fits", 210, 52, "9", 9, false},
		{"laptop steps 9 down to 4", laptopW, laptopH, "9", 4, true},
		{"laptop steps 6 down to 4", laptopW, laptopH, "6", 4, true},
		{"laptop keeps 4", laptopW, laptopH, "4", 4, false},
		{"split screen keeps one", splitW, splitH, "4", 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, w, _ := setupWallSize(t, 0, tc.w, tc.h)
			c.Run("set-option", "-w", "-t", "f:main", "@fleet_wall", tc.preset)
			s, err := w.Read()
			if err != nil {
				t.Fatal(err)
			}
			if got := s.Slots(); got != tc.wantSlots {
				t.Errorf("%dx%d preset %s: %d slots, want %d", tc.w, tc.h, tc.preset, got, tc.wantSlots)
			}
			if got := s.Capped(); got != tc.wantCapped {
				t.Errorf("%dx%d preset %s: capped=%v, want %v", tc.w, tc.h, tc.preset, got, tc.wantCapped)
			}
		})
	}
}

// M-9 on a laptop puts four panes up, not nine, and leaves the preset alone so
// a bigger window gets all nine back.
func TestSetPresetStepsDownOnASmallWindow(t *testing.T) {
	_, w, agents := setupWallSize(t, 6, laptopW, laptopH)
	for _, p := range agents[:2] {
		noTarget(t, w)
		if _, err := w.Show(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.SetPreset("9"); err != nil {
		t.Fatal(err)
	}
	if on := w.OnWall(); len(on) != 4 {
		t.Fatalf("M-9 on a %dx%d window put %d panes up, want 4: %v", laptopW, laptopH, len(on), on)
	}
	if got := w.opt("@fleet_wall"); got != "9" {
		t.Errorf("preset rewritten to %q; it must stay 9 so a bigger window gets 9 back", got)
	}
}

// Growing the terminal takes the cap back up without touching the preset.
func TestFloorRecomputesOnResize(t *testing.T) {
	c, w, agents := setupWallSize(t, 6, 300, 80)
	for _, p := range agents {
		noTarget(t, w)
		if _, err := w.Show(p); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if err := w.SetPreset("9"); err != nil {
		t.Fatal(err)
	}
	if on := w.OnWall(); len(on) != 6 {
		t.Fatalf("six agents on a 300x80 wall: %v", on)
	}
	resize(t, c, laptopW, laptopH)
	if err := w.Apply(); err != nil {
		t.Fatal(err)
	}
	if on := w.OnWall(); len(on) != 4 {
		t.Fatalf("after shrinking to %dx%d: %d panes, want 4: %v", laptopW, laptopH, len(on), on)
	}
	resize(t, c, 300, 80)
	if err := w.Apply(); err != nil {
		t.Fatal(err)
	}
	s, _ := w.Read()
	if s.Slots() != 9 || s.Capped() {
		t.Errorf("back at 300x80: %d slots, capped=%v, want 9 and false", s.Slots(), s.Capped())
	}
}

// A pane that needs me is never shed by the floor (ADR-14 still wins): the
// wall goes cramped instead of hiding it.
func TestFloorKeepsProtectedPanes(t *testing.T) {
	c, w, agents := setupWallSize(t, 6, 300, 80)
	for _, p := range agents[:5] {
		c.SetPaneOption(p, "@fleet_status", "waiting")
		c.SetPaneOption(p, "@fleet_status_since", "1")
	}
	if err := w.SetPreset("9"); err != nil {
		t.Fatal(err)
	}
	resize(t, c, splitW, splitH)
	if err := w.Apply(); err != nil {
		t.Fatal(err)
	}
	on := w.OnWall()
	if len(on) != 5 {
		t.Fatalf("five waiting panes must stay even under the floor, got %d: %v", len(on), on)
	}
}

// Add steps the preset up until the window, not the preset, is what is full.
func TestAddRefusesBelowTheFloor(t *testing.T) {
	c, w, agents := setupWallSize(t, 6, laptopW, laptopH)
	c.Run("set-option", "-w", "-t", "f:main", "@fleet_wall", "1")
	added := 0
	for _, p := range agents {
		ok, err := w.Add(p)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			added++
		}
		time.Sleep(2 * time.Millisecond)
	}
	if added != 4 {
		t.Fatalf("Add accepted %d panes on a %dx%d window, want 4", added, laptopW, laptopH)
	}
	if on := w.OnWall(); len(on) != 4 {
		t.Fatalf("on wall: %v", on)
	}
}

func resize(t *testing.T, c *tmux.Client, x, y int) {
	t.Helper()
	if _, err := c.Run("resize-window", "-t", "f:main", "-x", strconv.Itoa(x), "-y", strconv.Itoa(y)); err != nil {
		t.Fatalf("resize-window: %v", err)
	}
}

// The floor never sheds the ▶ target pane: an explicit choice beats it
// (ADR-31), even on a wall held over the cap by protected panes.
func TestFloorKeepsTheTargetPane(t *testing.T) {
	c, w, agents := setupWallSize(t, 4, 300, 80)
	for _, p := range agents[:3] {
		c.SetPaneOption(p, "@fleet_status", "waiting")
		c.SetPaneOption(p, "@fleet_status_since", "1")
	}
	if err := w.SetPreset("9"); err != nil {
		t.Fatal(err)
	}
	// agents[3] is the only unprotected pane; make it the target, as a card
	// into the ▶ slot would.
	if _, err := w.Show(agents[3]); err != nil {
		t.Fatal(err)
	}
	resize(t, c, splitW, splitH)
	if err := w.Apply(); err != nil {
		t.Fatal(err)
	}
	on := w.OnWall()
	if !on["a3"] {
		t.Fatalf("the target pane was shed by the floor: %v", on)
	}
	if len(on) != 4 {
		t.Fatalf("three waiting panes plus the target must stay, got %v", on)
	}
}
