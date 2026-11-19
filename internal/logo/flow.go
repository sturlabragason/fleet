package logo

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/sturlabragason/fleet/internal/theme"
)

// The mark's geometry in the 96x96 icon's units, as scripts/build_logo.py
// draws it: five strands enter tangled at flowStarts and resolve onto
// flowLanes at x=52, run flat to the square stops at x=80; the third is the
// accent lane.
var (
	flowStarts = [5]float64{70, 28, 62, 36, 54}
	flowLanes  = [5]float64{28, 38, 48, 58, 68}
)

const (
	flowAccent = 2
	flowX0     = 8.0  // where the strands enter
	flowFlat   = 52.0 // where they lie on their lanes
	flowEnd    = 80.0 // the square stops
	// flowY0..flowY1 is the band of the icon stretched over the pane's
	// height: the lanes and the strands' widest sway, edge to edge.
	flowY0 = 22.0
	flowY1 = 74.0
)

// Motion (ADR-176). Every speed is slow enough that a strand moves under
// half a row between two frames at 4 fps on a 45-row pane, so the anti-
// aliased edge carries it smoothly instead of stepping (TestFlowSmooth).
const (
	swayAmp    = 4.5  // icon units an entry rises and falls
	swayPeriod = 18.0 // seconds per rise and fall
	waveAmp    = 1.6  // icon units of the ripple running along each strand
	waveLen    = 30.0 // icon units per ripple
	wavePeriod = 14.0 // seconds for a ripple to travel one waveLen
	breathe    = 5.0  // seconds per breath of a lane and its square
	leanPull   = 0.22 // how far the entries lean toward the selected tile
)

// FlowLevels is how many brightness steps each colour role has.
const FlowLevels = 8

// FlowCell is one cell of a flow frame: the glyph and its colour index into
// FlowColours, -1 for a blank cell.
type FlowCell struct {
	R   rune
	Col int8
}

// Lean is where the strands gather: Row is a row of the flow's grid and
// Pull from 0 (none) to 1 (full leanPull). The caller eases both so a new
// selection draws the strands over gently.
type Lean struct{ Row, Pull float64 }

// Flow is the fleet mark stretched edge to edge over a w×h cell grid, drawn
// as a live field: the strands on the left sway and ripple like ribbons, the
// lanes and their squares on the right never move and breathe in colour only.
type Flow struct {
	w, h         int
	ky           float64 // rows per icon unit
	xc           float64 // column of the squares' centre
	hwInk, hwAcc float64 // ribbon half thickness, rows
	sqInk, sqAcc float64 // square half height, rows (half width is twice it in columns)
	xs           []float64
	lanes        [5]float64   // each lane's height, snapped to a row's centre so it draws crisp
	ys           [5][]float64 // scratch: each strand's centre row per column
	val          []float64    // scratch: brightness per cell
	role         []int8       // scratch: 0 ink, 1 accent
	lane         []int8       // scratch: the lane whose breath a cell takes, -1 none
}

// NewFlow fits the mark to w×h cells.
func NewFlow(w, h int) *Flow {
	w, h = max(w, 1), max(h, 1)
	f := &Flow{w: w, h: h, ky: float64(h) / (flowY1 - flowY0),
		val: make([]float64, w*h), role: make([]int8, w*h), lane: make([]int8, w*h), xs: make([]float64, w)}
	sp := 10 * f.ky // rows between two lanes
	f.hwInk, f.hwAcc = max(0.8, 0.13*sp), max(1.1, 0.18*sp)
	f.sqInk, f.sqAcc = max(0.8, 0.2*sp), max(1.0, 0.25*sp)
	f.xc = max(float64(w)-2.5-2*f.sqAcc, 1) // the accent square ends two cells short of the edge
	for c := range f.xs {
		f.xs[c] = flowX0 + (flowEnd-flowX0)*(float64(c)+0.5)/f.xc
	}
	for i := range f.ys {
		f.ys[i] = make([]float64, w)
		f.lanes[i] = flowY0 + (math.Floor(f.row(flowLanes[i]))+0.5)/f.ky
	}
	return f
}

func (f *Flow) row(y float64) float64 { return (y - flowY0) * f.ky }

// entry is strand i's entry height at time t, leaning toward lean.
func (f *Flow) entry(i int, t float64, lean Lean) float64 {
	y := flowStarts[i] + swayAmp*math.Sin(2*math.Pi*t/swayPeriod+float64(i)*1.26)
	target := flowY0 + lean.Row/f.ky
	return y + leanPull*lean.Pull*(target-y)
}

// centre fills f.ys[i] with strand i's centre row at every column: the
// brand's two cubics from the swaying entry to the lane, plus a ripple that
// travels rightwards and fades out before the strand lies flat.
func (f *Flow) centre(i int, t float64, lean Lean) {
	a, b, s := f.entry(i, t, lean), f.lanes[i], f.entry((i+2)%5, t, lean)
	m := (a + b) / 2
	segs := [2][4][2]float64{
		{{flowX0, a}, {22, a}, {26, s}, {34, m}},
		{{34, m}, {42, 2*m - s}, {44, b}, {flowFlat, b}},
	}
	ys := f.ys[i]
	c := 0
	for _, seg := range segs {
		n := int(float64(f.w)*(seg[3][0]-seg[0][0])/(flowEnd-flowX0))*2 + 8
		px, py := bez(seg, 0)
		for j := 1; j <= n; j++ {
			x, y := bez(seg, float64(j)/float64(n))
			for c < f.w && f.xs[c] <= x {
				u := 0.0
				if x > px {
					u = (f.xs[c] - px) / (x - px)
				}
				ys[c] = py + (y-py)*u
				c++
			}
			px, py = x, y
		}
	}
	for ; c < f.w; c++ {
		ys[c] = b
	}
	for c := range ys {
		x := f.xs[c]
		env := 0.0
		if x < 44 {
			env = 0.5 + 0.5*math.Cos(math.Pi*max(0, x-flowX0)/(44-flowX0))
		}
		ripple := waveAmp * env * math.Sin(2*math.Pi*(x/waveLen-t/wavePeriod)+float64(i)*2.1)
		ys[c] = f.row(ys[c] + ripple)
	}
}

func bez(p [4][2]float64, t float64) (float64, float64) {
	u := 1 - t
	a, b, c, d := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
	return a*p[0][0] + b*p[1][0] + c*p[2][0] + d*p[3][0], a*p[0][1] + b*p[1][1] + c*p[2][1] + d*p[3][1]
}

// Frame draws the mark at time t (seconds) into out (w*h cells). The glyph
// of a cell comes from the shape alone, so the lanes and squares keep every
// glyph from frame to frame; their breath changes only the colour index.
func (f *Flow) Frame(t float64, lean Lean, out []FlowCell) {
	clear(f.val) // every cell lit below was written by put, lane included
	var breath [5]float64
	for i := range breath {
		p := 0.5 + 0.5*math.Sin(2*math.Pi*t/breathe-float64(i)*0.7)
		breath[i] = 0.7 + 0.3*p*p
	}
	for _, i := range [5]int{0, 4, 1, 3, flowAccent} { // back to front; the accent on top
		f.centre(i, t, lean)
		f.ribbon(i)
		f.square(i)
	}
	for k, v := range f.val {
		g := flowGlyph(v)
		if g == ' ' {
			out[k] = FlowCell{' ', -1}
			continue
		}
		lv := min(v, 0.999)
		if l := f.lane[k]; l >= 0 {
			lv *= breath[l]
		}
		out[k] = FlowCell{g, f.role[k]*FlowLevels + int8(lv*FlowLevels)}
	}
}

// flowGlyph is a cell's glyph from how deep inside a ribbon it is: three
// steps, so a moving strand changes a cell's glyph only when its edge
// crosses a step; the smooth part of the motion is carried by the colour.
// The same characters are ASCII, so no fallback is needed.
func flowGlyph(v float64) rune {
	switch {
	case v >= 0.6:
		return '@'
	case v >= 0.3:
		return '+'
	case v >= 0.12:
		return ':'
	}
	return ' '
}

// put lights cell k for strand i. Strands are drawn back to front, so a
// strand's body covers what is under it and its edge only brightens, and
// each crossing reads as one strand passing over another.
func (f *Flow) put(k int, v float64, i int, onLane bool) {
	acc := i == flowAccent
	if v < 0.5 && v <= f.val[k] {
		return
	}
	f.val[k] = v
	f.role[k] = 0
	if acc {
		f.role[k] = 1
	}
	f.lane[k] = -1
	if onLane {
		f.lane[k] = int8(i)
	}
}

// ribbon lights strand i along its centre line: full inside the ribbon and
// an anti-aliased edge one row wide, measured across the strand (a cell is
// about twice as tall as wide) so steep parts keep their thickness.
func (f *Flow) ribbon(i int) {
	hw := f.hwInk
	if i == flowAccent {
		hw = f.hwAcc
	}
	ys := f.ys[i]
	for c, y := range ys {
		slope := (ys[min(c+1, f.w-1)] - ys[max(c-1, 0)]) / 2
		cos := 1 / math.Sqrt(1+4*slope*slope)
		reach := (hw+1)/cos + 1
		onLane := f.xs[c] >= flowFlat
		for r := max(int(y-reach), 0); r <= min(int(y+reach), f.h-1); r++ {
			d := math.Abs(float64(r)+0.5-y) * cos
			if v := min(max(hw+0.5-d, 0), 1); v > 0 {
				f.put(r*f.w+c, v, i, onLane)
			}
		}
	}
}

// square lights lane i's stop: a square (twice as many columns as rows) on
// the lane, its edges anti-aliased.
func (f *Flow) square(i int) {
	sh := f.sqInk
	if i == flowAccent {
		sh = f.sqAcc
	}
	sw, cy := 2*sh, f.row(f.lanes[i])
	for r := max(int(cy-sh-1), 0); r <= min(int(cy+sh+1), f.h-1); r++ {
		vy := min(max(sh+0.5-math.Abs(float64(r)+0.5-cy), 0), 1)
		for c := max(int(f.xc-sw-1), 0); c < f.w; c++ {
			if v := vy * min(max(sw+0.5-math.Abs(float64(c)+0.5-f.xc), 0), 1); v > 0 {
				f.put(r*f.w+c, v, i, true)
			}
		}
	}
}

// FlowColours is the colour table for the active theme, FlowLevels steps
// per role (ink, accent), each from the ground (theme.Base) toward its ink
// and scaled by dim, so tiles drawn over the flow stay the brightest thing.
func FlowColours(dim float64) []string {
	out := make([]string, 0, 2*FlowLevels)
	// The ink strands stop short of full Text, so the accent stands out in
	// every theme, Lane's pale blue on near-white included.
	for _, top := range []string{theme.Mix(theme.Base, theme.Text, 0.75), theme.Select} {
		for l := range FlowLevels {
			out = append(out, theme.Mix(theme.Base, top, dim*(0.18+0.82*float64(l+1)/FlowLevels)))
		}
	}
	return out
}

// FlowEscapes is the escape that starts each FlowColours colour in the
// terminal's own colour profile, nil when it has no colour (NO_COLOR, a
// test, a dumb terminal): then there is nothing to draw.
func FlowEscapes(dim float64) []string {
	cols := FlowColours(dim)
	out := make([]string, len(cols))
	for i, c := range cols {
		s := lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render("x")
		pre, _, _ := strings.Cut(s, "x")
		if pre == "" {
			return nil
		}
		out[i] = pre
	}
	return out
}
