package layout

import "testing"

// screenCase is one terminal size and what the floor must make of it.
type screenCase struct {
	name           string
	winW, winH     int // terminal, minus the tmux status line
	wallW, wallH   int // the wall rectangle left after admiral and overview
	floorW, floorH int
	maxPanes       int
}

// screens are real terminal sizes in cells, admiral shown, default overview.
var screens = []screenCase{
	{"laptop 13\"", 160, 42, 95, 27, 40, 10, 4},
	{"1080p", 210, 52, 145, 37, 40, 10, 9},
	{"1440p", 280, 70, 189, 55, 47, 13, 9},
	{"ultrawide", 340, 80, 249, 65, 62, 16, 9},
	{"reference 300x80", 300, 79, 209, 64, 52, 16, 9},
	{"split screen", 120, 35, 55, 20, 40, 10, 1},
	{"tiny", 80, 24, 39, 9, 40, 10, 1},
}

func TestFloorScalesWithTheScreen(t *testing.T) {
	for _, s := range screens {
		w, h := Default.WallRect(s.winW, s.winH, true)
		if w != s.wallW || h != s.wallH {
			t.Errorf("%s: wall %dx%d, want %dx%d", s.name, w, h, s.wallW, s.wallH)
		}
		fw, fh := Default.Floor(w, h)
		if fw != s.floorW || fh != s.floorH {
			t.Errorf("%s: floor %dx%d, want %dx%d", s.name, fw, fh, s.floorW, s.floorH)
		}
		if fw < Default.FloorMinW || fh < Default.FloorMinH {
			t.Errorf("%s: floor %dx%d is under the absolute bound", s.name, fw, fh)
		}
		if fw > Default.MinPaneW || fh > Default.MinPaneH {
			t.Errorf("%s: floor %dx%d is above the comfort minimum", s.name, fw, fh)
		}
	}
}

// A bigger screen must never ask for a smaller pane than a smaller one.
func TestFloorIsMonotoneInScreenSize(t *testing.T) {
	prevW, prevH := 0, 0
	for _, w := range []int{40, 60, 100, 150, 209, 249, 400} {
		fw, fh := Default.Floor(w, w/3)
		if fw < prevW || fh < prevH {
			t.Errorf("wall width %d: floor %dx%d shrank from %dx%d", w, fw, fh, prevW, prevH)
		}
		prevW, prevH = fw, fh
	}
}

func TestMaxPanesPerScreen(t *testing.T) {
	for _, s := range screens {
		w, h := Default.WallRect(s.winW, s.winH, true)
		if got := Default.MaxPanes("9", w, h); got != s.maxPanes {
			t.Errorf("%s (%dx%d wall): max %d panes, want %d", s.name, w, h, got, s.maxPanes)
		}
	}
}

// Whatever MaxPanes allows must really clear the floor, and one more must not.
func TestMaxPanesIsTheLastShapeThatClearsTheFloor(t *testing.T) {
	for _, s := range screens {
		w, h := Default.WallRect(s.winW, s.winH, true)
		n := Default.MaxPanes("9", w, h)
		if n < 1 {
			t.Errorf("%s: max %d panes, the wall must always show one", s.name, n)
		}
		if !Default.Fits("9", n, w, h) {
			t.Errorf("%s: %d panes do not clear the floor", s.name, n)
		}
		if n < maxSlots() && Default.Fits("9", n+1, w, h) {
			t.Errorf("%s: %d panes also fit, max should be higher", s.name, n+1)
		}
	}
}

// The step-down: a preset is a cap, and the floor lowers that cap.
func TestFitSlotsStepsThePresetDown(t *testing.T) {
	cases := []struct {
		screen string
		preset string
		want   int
	}{
		{"laptop 13\"", "9", 4},
		{"laptop 13\"", "6", 4},
		{"laptop 13\"", "4", 4},
		{"laptop 13\"", "2", 2},
		{"1080p", "9", 9},
		{"1440p", "9", 9},
		{"ultrawide", "9", 9},
		{"split screen", "9", 1},
		{"split screen", "4", 1},
		{"tiny", "2", 1},
	}
	for _, tc := range cases {
		s := screen(t, tc.screen)
		w, h := Default.WallRect(s.winW, s.winH, true)
		got := Default.FitSlots(tc.preset, w, h)
		if got != tc.want {
			t.Errorf("%s preset %s: %d slots, want %d", tc.screen, tc.preset, got, tc.want)
		}
		if got > Default.Slots(tc.preset) {
			t.Errorf("%s preset %s: the floor raised the cap to %d", tc.screen, tc.preset, got)
		}
	}
}

// Growing the window takes the cap back up: the floor recomputes on resize.
func TestFitSlotsRecoversWhenTheWindowGrows(t *testing.T) {
	small := screen(t, "laptop 13\"")
	w, h := Default.WallRect(small.winW, small.winH, true)
	if n := Default.FitSlots("9", w, h); n != 4 {
		t.Fatalf("laptop: %d slots, want 4", n)
	}
	big := screen(t, "ultrawide")
	w, h = Default.WallRect(big.winW, big.winH, true)
	if n := Default.FitSlots("9", w, h); n != 9 {
		t.Fatalf("after resize to ultrawide: %d slots, want 9", n)
	}
}

// Every pane of a floor-capped wall really is at or above the floor.
func TestCappedWallNeverRendersUnderTheFloor(t *testing.T) {
	for _, s := range screens {
		w, h := Default.WallRect(s.winW, s.winH, true)
		n := Default.FitSlots("9", w, h)
		if n < 2 {
			continue
		}
		fw, fh := Default.Floor(w, h)
		var ids []string
		for i := 0; i < n; i++ {
			ids = append(ids, "%"+string(rune('a'+i)))
		}
		r := paneRects(t, Input{W: s.winW, H: s.winH, Sups: []string{"%1"}, Ov: "%2", Wall: ids, Preset: "9"})
		for _, id := range ids {
			if p := r[id]; p.W < fw || p.H < fh {
				t.Errorf("%s: pane %s is %dx%d, under the %dx%d floor", s.name, id, p.W, p.H, fw, fh)
			}
		}
	}
}

func screen(t *testing.T, name string) screenCase {
	t.Helper()
	for _, s := range screens {
		if s.name == name {
			return s
		}
	}
	t.Fatalf("no screen %q", name)
	return screens[0]
}

// The grid must never render a floor-breaking shape while a floor-clearing
// one exists. A 77×21 wall scores two side-by-side 38-column panes slightly
// higher than two stacked ones, but 38 is under the floor.
func TestGridPrefersFloorClearingShapes(t *testing.T) {
	for w := 40; w <= 260; w += 7 {
		for h := 12; h <= 70; h += 5 {
			fw, fh := Default.Floor(w, h)
			for n := 2; n <= maxSlots(); n++ {
				if !Default.Fits("9", n, w, h) {
					continue
				}
				perRow, _ := gridShape(n, w, h, Default)
				rows, cols := len(perRow), 0
				for _, c := range perRow {
					if c > cols {
						cols = c
					}
				}
				cellW, cellH := (w-(cols-1))/cols, (h-(rows-1))/rows
				if cellW < fw || cellH < fh {
					t.Fatalf("%dx%d wall, %d panes: %v gives %dx%d cells, under the %dx%d floor",
						w, h, n, perRow, cellW, cellH, fw, fh)
				}
			}
		}
	}
}

// The main+stack preset has its own shape, so it needs its own floor check:
// the stack column is only 40% of the wall, which a grid check would miss.
func TestMainStackPresetRespectsTheFloor(t *testing.T) {
	cases := []struct {
		screen string
		want   int
	}{
		{"reference 300x80", 4},
		{"ultrawide", 4},
		{"laptop 13\"", 1}, // a 38-column stack is under the floor
	}
	for _, tc := range cases {
		s := screen(t, tc.screen)
		w, h := Default.WallRect(s.winW, s.winH, true)
		if got := Default.FitSlots("m", w, h); got != tc.want {
			t.Errorf("%s preset m: %d slots, want %d", tc.screen, got, tc.want)
		}
	}
	// and what it does allow really clears the floor once rendered
	for _, s := range screens {
		w, h := Default.WallRect(s.winW, s.winH, true)
		n := Default.FitSlots("m", w, h)
		if n < 2 {
			continue
		}
		fw, fh := Default.Floor(w, h)
		var ids []string
		for i := 0; i < n; i++ {
			ids = append(ids, "%"+string(rune('a'+i)))
		}
		in := Input{W: s.winW, H: s.winH, Sups: []string{"%1"}, Ov: "%2", Wall: ids, Preset: "m"}
		for id, p := range paneRects(t, in) {
			if id == "%1" || id == "%2" {
				continue
			}
			if p.W < fw || p.H < fh {
				t.Errorf("%s preset m: pane %s is %dx%d, under the %dx%d floor", s.name, id, p.W, p.H, fw, fh)
			}
		}
	}
}
