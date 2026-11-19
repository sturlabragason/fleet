package logo

import (
	"math"
	"testing"
)

// flowRun draws a w×h flow at 12 fps for secs seconds, the strands leaning
// toward a tile near the top for the second half, and calls each with the
// frame's time, its cells and the strands' centre rows.
func flowRun(t *testing.T, w, h int, secs float64, each func(tt float64, cells []FlowCell, f *Flow)) {
	t.Helper()
	f := NewFlow(w, h)
	cells := make([]FlowCell, w*h)
	for n := 0; float64(n) <= secs*12; n++ {
		tt := float64(n) / 12
		lean := Lean{}
		if tt > secs/2 {
			lean = Lean{Row: float64(h) * 0.2, Pull: min(1, (tt-secs/2)/8)}
		}
		f.Frame(tt, lean, cells)
		each(tt, cells, f)
	}
}

// TestFlowRightFixed: the lanes and squares keep every glyph in every
// frame, sway, ripple and lean included; only their colour breathes.
func TestFlowRightFixed(t *testing.T) {
	for _, sz := range [][2]int{{160, 44}, {80, 20}, {220, 60}} {
		w, h := sz[0], sz[1]
		var first []FlowCell
		breathed := false
		flowRun(t, w, h, 30, func(tt float64, cells []FlowCell, f *Flow) {
			if first == nil {
				first = append([]FlowCell(nil), cells...)
				return
			}
			for k, c := range cells {
				if f.xs[k%w] < flowFlat {
					continue
				}
				if c.R != first[k].R {
					t.Fatalf("%dx%d t=%.2f: right cell %d,%d %q, was %q", w, h, tt, k%w, k/w, c.R, first[k].R)
				}
				breathed = breathed || c.Col != first[k].Col
			}
		})
		if !breathed {
			t.Errorf("%dx%d: the lanes never changed colour", w, h)
		}
	}
}

// TestFlowContinuous: every strand is one unbroken ribbon in every frame:
// each column of it is lit at its centre and touches the next column.
func TestFlowContinuous(t *testing.T) {
	flowRun(t, 160, 44, 30, func(tt float64, cells []FlowCell, f *Flow) {
		for i, ys := range f.ys {
			hw := f.hwInk
			if i == flowAccent {
				hw = f.hwAcc
			}
			for c, y := range ys {
				if r := int(y); r >= 0 && r < f.h && cells[r*f.w+c].R == ' ' {
					t.Fatalf("t=%.2f strand %d column %d: blank at its centre row %d", tt, i, c, r)
				}
				if c+1 < len(ys) && math.Abs(ys[c+1]-y) > 2*hw+1 {
					t.Fatalf("t=%.2f strand %d: gap between columns %d and %d (%.2f rows)", tt, i, c, c+1, ys[c+1]-y)
				}
			}
		}
	})
}

// TestFlowSmooth: between two frames at 12 fps no strand moves a quarter
// row or more anywhere, so the anti-aliased edge carries the motion as a
// fade instead of a visible step.
func TestFlowSmooth(t *testing.T) {
	var prev [5][]float64
	worst := 0.0
	flowRun(t, 160, 44, 30, func(_ float64, _ []FlowCell, f *Flow) {
		for i, ys := range f.ys {
			if prev[i] != nil {
				for c, y := range ys {
					worst = max(worst, math.Abs(y-prev[i][c]))
				}
			}
			prev[i] = append(prev[i][:0], ys...)
		}
	})
	if worst >= 0.25 {
		t.Errorf("a strand moved %.2f rows in one frame, want under 0.25", worst)
	}
}

// TestFlowFills: the mark reaches every edge of the grid: lit cells in the
// top and bottom rows' band, at the left edge and at the right edge.
func TestFlowFills(t *testing.T) {
	for _, sz := range [][2]int{{160, 44}, {90, 24}, {240, 70}} {
		w, h := sz[0], sz[1]
		f := NewFlow(w, h)
		cells := make([]FlowCell, w*h)
		f.Frame(0, Lean{}, cells)
		lit := func(c0, c1, r0, r1 int) bool {
			for r := r0; r < r1; r++ {
				for c := c0; c < c1; c++ {
					if cells[r*w+c].R != ' ' {
						return true
					}
				}
			}
			return false
		}
		band := max(h/8, 2)
		if !lit(0, w, 0, band) || !lit(0, w, h-band, h) || !lit(0, 2, 0, h) || !lit(w-2, w, 0, h) {
			t.Errorf("%dx%d: the mark leaves an empty edge", w, h)
		}
	}
}

// TestFlowTiny: a pane of a few cells draws without panicking and keeps
// its columns in order.
func TestFlowTiny(t *testing.T) {
	for _, sz := range [][2]int{{1, 1}, {3, 2}, {5, 27}, {2, 40}, {40, 1}} {
		f := NewFlow(sz[0], sz[1])
		f.Frame(3, Lean{Row: 1, Pull: 1}, make([]FlowCell, sz[0]*sz[1]))
		for c := 1; c < len(f.xs); c++ {
			if !(f.xs[c] > f.xs[c-1]) || math.IsInf(f.xs[c], 0) {
				t.Fatalf("%dx%d: columns out of order: %v", sz[0], sz[1], f.xs)
			}
		}
	}
}

// TestFlowVaried: strands roam widely across the canvas, do not repeat their
// starting state within two minutes, move independently, and different seeds
// trace distinct paths.
func TestFlowVaried(t *testing.T) {
	f := NewFlowSeeded(160, 44, 42)
	var start, lo, hi [5]float64
	for i := range start {
		start[i] = f.strands[i].y0.pos
		lo[i], hi[i] = start[i], start[i]
	}
	var at3, at4 [5]float64
	for n := 1; n <= 120*12; n++ {
		tt := float64(n) / 12
		f.advance(tt)
		near := tt >= 5
		for i := range start {
			y := f.strands[i].y0.pos
			lo[i], hi[i] = min(lo[i], y), max(hi[i], y)
			near = near && math.Abs(y-start[i]) < 0.25 && math.Abs(f.strands[i].y1.pos-start[i]) < 0.25
		}
		if near {
			t.Fatalf("the wandering strands returned to their start after %.1f s", tt)
		}
		if n == 3*12 {
			for i := range at3 {
				at3[i] = f.strands[i].y0.pos
			}
		}
		if n == 4*12 {
			for i := range at4 {
				at4[i] = f.strands[i].y0.pos
			}
		}
	}
	for i := range lo {
		if hi[i]-lo[i] < 12 {
			t.Errorf("strand %d only roamed %.1f icon units, want >= 12", i, hi[i]-lo[i])
		}
	}
	same := 0
	for i := 1; i < 5; i++ {
		if math.Signbit(at4[i]-at3[i]) == math.Signbit(at4[0]-at3[0]) {
			same++
		}
	}
	if same == 4 {
		t.Error("all strands move the same way at once")
	}
	f2 := NewFlowSeeded(160, 44, 99)
	f2.advance(10)
	f1 := NewFlowSeeded(160, 44, 42)
	f1.advance(10)
	if math.Abs(f1.strands[0].y0.pos-f2.strands[0].y0.pos) < 0.01 {
		t.Error("different seeds traced the same path")
	}
}
