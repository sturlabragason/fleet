package logo

import (
	"math"
	"math/rand/v2"
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

	leanPull = 0.22 // how far the entries lean toward the selected tile
)

// FlowLevels is how many brightness steps each colour role has: enough that
// a strand's edge moving a fraction of a row reads as a fade, not a jump.
const FlowLevels = 16

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

// wander is one degree of freedom that roams [lo, hi] like a classic Bezier
// screensaver (ADR-181): it picks random target velocities at random intervals,
// turns smoothly off the walls with a fresh rebound speed, and eases its
// velocity toward the target so motion stays C¹ smooth and non-repeating.
type wander struct {
	pos, vel, tgt float64
	lo, hi, vmax  float64
	hold          float64
}

func (w *wander) step(dt float64, rng *rand.Rand) {
	w.hold -= dt
	if w.hold <= 0 {
		mag := (0.25 + 0.75*rng.Float64()) * w.vmax
		if rng.IntN(2) == 0 {
			mag = -mag
		}
		w.tgt = mag
		w.hold = 2.5 + rng.Float64()*6.5
	}
	margin := (w.hi - w.lo) * 0.14
	if w.pos < w.lo+margin && w.tgt <= 0 {
		w.tgt = (0.35 + 0.65*rng.Float64()) * w.vmax
		w.hold = 2.5 + rng.Float64()*5.5
	} else if w.pos > w.hi-margin && w.tgt >= 0 {
		w.tgt = -(0.35 + 0.65*rng.Float64()) * w.vmax
		w.hold = 2.5 + rng.Float64()*5.5
	}
	w.vel += (w.tgt - w.vel) * min(1, dt*0.85)
	w.vel = min(max(w.vel, -w.vmax), w.vmax)
	w.pos = min(max(w.pos+w.vel*dt, w.lo), w.hi)
}

// strandState holds the freely wandering Bezier control points and lane breath
// of one strand: entry height y0, two intermediate bend points (x1, y1) and
// (x2, y2), and the right-hand lane's brightness breath.
type strandState struct {
	y0, x1, y1, x2, y2, breath wander
}

// Flow is the fleet mark stretched edge to edge over a w×h cell grid, drawn
// as a live field: the strands on the left roam freely like Bezier ribbons and
// weave into the fixed lanes and breathing squares on the right.
type Flow struct {
	w, h         int
	seed         uint64
	t            float64
	rng          *rand.Rand
	strands      [5]strandState
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

// NewFlow fits the mark to w×h cells with a deterministic default seed.
func NewFlow(w, h int) *Flow { return NewFlowSeeded(w, h, 1) }

// NewFlowSeeded fits the mark to w×h cells with seed driving the wandering
// control points, so each live session traces its own non-repeating path.
func NewFlowSeeded(w, h int, seed uint64) *Flow {
	w, h = max(w, 1), max(h, 1)
	f := &Flow{w: w, h: h, seed: seed, ky: float64(h) / (flowY1 - flowY0),
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
	f.reset()
	return f
}

// reset places the strands at the canonical brand mark (t=0) and reseeds rng.
func (f *Flow) reset() {
	f.t = 0
	f.rng = rand.New(rand.NewPCG(f.seed, f.seed^0x9e3779b97f4a7c15))
	for i := range f.strands {
		a, s := flowStarts[i], flowStarts[(i+2)%5]
		init := func(pos, lo, hi, vmax float64) wander {
			dir := 1.0
			if f.rng.IntN(2) == 0 {
				dir = -1.0
			}
			tgt := dir * (0.35 + 0.65*f.rng.Float64()) * vmax
			return wander{pos: pos, vel: tgt * 0.4, tgt: tgt, lo: lo, hi: hi, vmax: vmax, hold: 1.5 + f.rng.Float64()*5}
		}
		f.strands[i] = strandState{
			y0:     init(a, 24, 72, 2.1),
			x1:     init(20, 14, 24, 0.55),
			y1:     init(a, 18, 78, 2.2),
			x2:     init(30, 27, 38, 0.65),
			y2:     init(s, 22, 74, 2.0),
			breath: init(0.85, 0.65, 1.0, 0.14),
		}
	}
}

// advance steps all wandering control points smoothly from f.t to t.
func (f *Flow) advance(t float64) {
	if t < f.t {
		f.reset()
	}
	dt := t - f.t
	if dt <= 0 {
		return
	}
	dt = min(dt, 20.0) // cap big jumps (e.g. startup warm-up or wake from sleep)
	const maxStep = 0.1
	for dt > 0 {
		h := min(dt, maxStep)
		for i := range f.strands {
			s := &f.strands[i]
			s.y0.step(h, f.rng)
			s.x1.step(h, f.rng)
			s.y1.step(h, f.rng)
			s.x2.step(h, f.rng)
			s.y2.step(h, f.rng)
			s.breath.step(h, f.rng)
		}
		dt -= h
	}
	f.t = t
}

func (f *Flow) row(y float64) float64 { return (y - flowY0) * f.ky }

// centre fills f.ys[i] with strand i's centre row at every column: two C¹-joined
// cubics driven by the strand's wandering control points that resolve horizontally
// onto the fixed lane at flowFlat.
func (f *Flow) centre(i int, lean Lean) {
	s := &f.strands[i]
	target := flowY0 + lean.Row/f.ky
	pull := leanPull * lean.Pull
	y0 := s.y0.pos + pull*(target-s.y0.pos)
	y1 := s.y1.pos + pull*0.6*(target-s.y1.pos)
	y2 := s.y2.pos + pull*0.3*(target-s.y2.pos)
	b := f.lanes[i]
	x1, x2 := s.x1.pos, s.x2.pos
	x3 := (x2 + flowFlat) / 2
	xm, ym := (x2+x3)/2, (y2+b)/2
	// Because M = (P2 + (x3, b))/2, the mirrored tangent 2*M - P2 is (x3, b),
	// giving exact C¹ continuity at M and a flat arrival onto lane b at flowFlat.
	segs := [2][4][2]float64{
		{{flowX0, y0}, {x1, y1}, {x2, y2}, {xm, ym}},
		{{xm, ym}, {x3, b}, {(x3 + flowFlat) / 2, b}, {flowFlat, b}},
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
				ys[c] = f.row(py + (y-py)*u)
				c++
			}
			px, py = x, y
		}
	}
	rb := f.row(b)
	for ; c < f.w; c++ {
		ys[c] = rb
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
	f.advance(t)
	clear(f.val) // every cell lit below was written by put, lane included
	for _, i := range [5]int{0, 4, 1, 3, flowAccent} { // back to front; the accent on top
		f.centre(i, lean)
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
			lv *= f.strands[l].breath.pos
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
	case v >= 0.64:
		return '@'
	case v >= 0.32:
		return '+'
	case v >= 0.06:
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
			out = append(out, theme.Mix(theme.Base, top, dim*float64(l+1)/FlowLevels))
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
