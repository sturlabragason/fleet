package layout

import (
	"reflect"
	"strings"
	"testing"
)

// The reference wall rectangle: a 300x80 terminal is 300x79 after the tmux
// status line (so Build gets H=79), minus the 90-column admiral + border and the 14-row overview
// + border (DESIGN.md §5b.2).
const refW, refH = 209, 64

func TestWallRectMatchesReference(t *testing.T) {
	root := Default.Build(Input{W: 300, H: 79, Sups: []string{"%1"}, Ov: "%2", Wall: []string{"%3", "%4"}, Preset: "4"})
	wall := root.Kids[1].Kids[1]
	if wall.W != refW || wall.H != refH {
		t.Fatalf("wall rect %dx%d, want %dx%d", wall.W, wall.H, refW, refH)
	}
}

func TestGridShapes(t *testing.T) {
	cases := []struct {
		n    int
		want []int
	}{
		{1, []int{1}}, {2, []int{2}}, {3, []int{3}}, {4, []int{2, 2}},
		{5, []int{2, 3}}, {6, []int{3, 3}}, {7, []int{2, 2, 3}},
	}
	for _, tc := range cases {
		got, _ := gridShape(tc.n, refW, refH, Default)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("grid(%d) = %v, want %v", tc.n, got, tc.want)
		}
	}
}

func TestGridNarrowWallStacks(t *testing.T) {
	got, _ := gridShape(2, 100, refH, Default)
	if !reflect.DeepEqual(got, []int{1, 1}) {
		t.Errorf("2 panes on a 100-col wall = %v, want [1 1]", got)
	}
}

func TestGridNeverOneRowOfFive(t *testing.T) {
	got, _ := gridShape(5, refW, refH, Default)
	if len(got) == 1 {
		t.Errorf("5 panes in one row of %d cols: %v", refW/5, got)
	}
	// and every pane of the chosen shape clears the minimum
	rows, score := gridShape(5, refW, refH, Default)
	if score < 1 {
		t.Errorf("5 panes should fit at 209x64: %v score %.2f", rows, score)
	}
}

func TestGridInfeasibleStillReturnsRows(t *testing.T) {
	rows, score := gridShape(9, 80, 24, Default)
	if len(rows) == 0 || sum(rows) != 9 {
		t.Fatalf("rows %v", rows)
	}
	if score >= 1 {
		t.Errorf("9 panes in 80x24 should score < 1, got %.2f", score)
	}
}

func sum(xs []int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}

// paneRects renders a wall input and returns pane id -> rectangle.
func paneRects(t *testing.T, in Input) map[string]*Node {
	t.Helper()
	root := Default.Build(in)
	place(root)
	out := map[string]*Node{}
	var walk func(*Node)
	walk = func(n *Node) {
		if len(n.Kids) == 0 {
			out[n.Pane] = n
			return
		}
		for _, k := range n.Kids {
			walk(k)
		}
	}
	walk(root)
	return out
}

func wallInput(wall []string, focus string) Input {
	return Input{W: 300, H: 79, Sups: []string{"%1"}, Ov: "%2", Wall: wall, Preset: "4", Focus: focus}
}

func TestEmphasisGivesFocusPaneMostOfTheWall(t *testing.T) {
	wall := []string{"%3", "%4", "%5"}
	r := paneRects(t, wallInput(wall, "%4"))
	f := r["%4"]
	area := float64(f.W*f.H) / float64(refW*refH)
	if area < 0.5 || area > 0.65 {
		t.Errorf("focus pane %dx%d is %.0f%% of the wall, want ~60%%", f.W, f.H, area*100)
	}
	if f.W != refW {
		t.Errorf("stacked emphasis should keep full width, got %d", f.W)
	}
	for _, id := range []string{"%3", "%5"} {
		if n := r[id]; n.W < Default.MinPaneW || n.H < Default.MinPaneH {
			t.Errorf("pane %s is %dx%d, below the minimum", id, n.W, n.H)
		}
	}
}

func TestEmphasisKeepsOtherPanesInOrder(t *testing.T) {
	_, leaves := Default.Render(wallInput([]string{"%3", "%4", "%5"}, "%4"))
	if !reflect.DeepEqual(leaves, []string{"%1", "%2", "%4", "%3", "%5"}) {
		t.Errorf("leaves %v: focus first, others in wall order", leaves)
	}
}

func TestNoFocusIsAnEvenGrid(t *testing.T) {
	r := paneRects(t, wallInput([]string{"%3", "%4", "%5", "%6"}, ""))
	if r["%3"].W != r["%4"].W || abs(r["%3"].H-r["%5"].H) > 1 {
		t.Errorf("uneven grid: %+v %+v %+v", r["%3"], r["%4"], r["%5"])
	}
	if r["%3"].H >= refH/2+5 {
		t.Errorf("no focus, but pane %dx%d is oversized", r["%3"].W, r["%3"].H)
	}
}

func TestEmphasisSkippedWhenRemainderTooSmall(t *testing.T) {
	wall := []string{"%3", "%4", "%5", "%6", "%7"}
	with := paneRects(t, wallInput(wall, "%4"))
	without := paneRects(t, wallInput(wall, ""))
	if with["%4"].H != without["%4"].H || with["%4"].W != without["%4"].W {
		t.Errorf("emphasis should be skipped for 5 panes: %+v vs %+v", with["%4"], without["%4"])
	}
}

func TestEmphasisIgnoredForPresets1AndM(t *testing.T) {
	in := wallInput([]string{"%3", "%4", "%5"}, "%5")
	in.Preset = "m"
	r := paneRects(t, in)
	if r["%3"].W <= r["%5"].W {
		t.Errorf("preset m must keep main+stack, got %+v %+v", r["%3"], r["%5"])
	}
}

func TestRenderedEmphasisLayoutIsWellFormed(t *testing.T) {
	s, _ := Default.Render(wallInput([]string{"%3", "%4", "%5"}, "%4"))
	if !strings.HasPrefix(s, Checksum(s[5:])+",") {
		t.Errorf("checksum mismatch: %s", s)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
