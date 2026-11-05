package layout

// The readability floor under the wall grid (DESIGN.md §5b.4, ADR-41).
//
// MinPaneW/MinPaneH are the *comfort* minimum the grid scores against
// (grid.go). Below them a pane is cramped but still used, because a cramped
// pane beats a hidden agent (ADR-15). The floor is where that stops being
// true: a pane smaller than this is wrapped garbage, so the wall shows fewer
// panes instead of tiling ever smaller.
//
// The floor is not one number. A 95-column laptop wall could never fit two
// panes at the 64-column comfort minimum, and on a 249-column ultrawide a
// 40-column pane is needlessly mean. So it scales with the wall rectangle --
// 1/FloorDiv of it -- clamped between an absolute lower bound
// (FloorMinW/FloorMinH, where text stops being readable at all) and the
// comfort minimum, which it never exceeds.

// Floor returns the smallest pane size allowed in a w×h wall rectangle.
func (c Config) Floor(w, h int) (int, int) {
	return clamp(w/c.FloorDiv, c.FloorMinW, c.MinPaneW),
		clamp(h/c.FloorDiv, c.FloorMinH, c.MinPaneH)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Fits reports whether n panes fit a w×h wall rectangle without any of them
// dropping under the floor, in the shape the preset would render them in:
// main + stack for "m", the auto-grid otherwise.
func (c Config) Fits(preset string, n, w, h int) bool {
	if n <= 1 {
		return true // one pane is the whole wall; there is nothing to step down to
	}
	fw, fh := c.Floor(w, h)
	if preset == "m" {
		return c.mainFits(n, w, h, fw, fh)
	}
	_, _, ok := bestShape(n, w, h, c, fw, fh)
	return ok
}

// mainFits is Fits for the main+stack preset: one pane takes MainPercent of
// the width, the rest stack in the remainder.
func (c Config) mainFits(n, w, h, fw, fh int) bool {
	main := part(w, c.MainPercent)
	stack := w - 1 - main
	if main < fw || stack < fw || h < fh {
		return false
	}
	return (h-(n-2))/(n-1) >= fh
}

// MaxPanes is the largest number of panes a w×h wall rectangle can show above
// the floor in this preset's shape. It is never below 1: the wall always
// shows something.
func (c Config) MaxPanes(preset string, w, h int) int {
	// Fits is monotone in n for both shapes -- dropping a pane from a shape
	// never shrinks a cell, and the grid prefers floor-clearing shapes
	// (grid.go) -- so the first n that does not fit ends the search.
	for n := 2; n <= maxSlots(); n++ {
		if !c.Fits(preset, n, w, h) {
			return n - 1
		}
	}
	return maxSlots()
}

// FitSlots is the preset's slot cap, lowered to what the wall rectangle can
// show above the floor. This is the step-down: M-9 on a laptop is four panes.
func (c Config) FitSlots(preset string, w, h int) int {
	want := c.Slots(preset)
	if m := c.MaxPanes(preset, w, h); m < want {
		return m
	}
	return want
}

// maxSlots is the biggest preset, the most panes any wall ever asks for.
func maxSlots() int {
	n := 1
	for _, v := range Presets {
		if v > n {
			n = v
		}
	}
	return n
}

// supWidth is the admiral column width, never more than half the window.
func (c Config) supWidth(w int) int {
	if sw := c.SupCols(w); sw <= w-20 {
		return sw
	}
	return w / 2
}

// WallRect returns the wall rectangle of a window: the area the grid and the
// floor are measured against, after the admiral column and the overview rows.
// Build lays the same arithmetic out as nodes; both call this.
func (c Config) WallRect(w, h int, sup bool) (int, int) {
	if sup && !c.SupHidden {
		w = w - c.supWidth(w) - 1
	}
	oh := c.OvRows()
	if oh > h-4 {
		oh = h / 3
	}
	return w, h - oh - 1
}
