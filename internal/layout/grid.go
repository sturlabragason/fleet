package layout

// Grid shapes are computed from the wall rectangle, not from the preset
// (DESIGN.md §5b.2, ADR-15).

// evenBonus rewards shapes that divide evenly, so 6 panes stay 3+3.
const evenBonus = 1.1

// gridShape returns the panes-per-row of the best shape for n panes in a w×h
// rectangle, and that shape's score. Score is min(cellW/MinPaneW,
// cellH/MinPaneH) clamped at 1: once a cell clears the minimum, extra cells of
// slack are worth nothing, so a feasible wide row beats a feasible tall one.
// Highest score wins, ties go to fewer rows (height is the scarce axis).
// A score below 1 means nothing fits; the best shape is still returned.
//
// Shapes that clear the readability floor are considered first (ADR-41), so
// the grid never picks a floor-breaking shape while a floor-clearing one
// exists. That is also what makes Fits (floor.go) agree with the shape
// actually rendered.
func gridShape(n, w, h int, c Config) ([]int, float64) {
	if n <= 0 {
		return nil, 0
	}
	fw, fh := c.Floor(w, h)
	if rows, s, ok := bestShape(n, w, h, c, fw, fh); ok {
		return spread(n, rows), s
	}
	rows, s, _ := bestShape(n, w, h, c, 0, 0)
	return spread(n, rows), s
}

// bestShape is gridShape's search, restricted to shapes whose cells are at
// least minW×minH. ok is false when no shape passes that restriction.
func bestShape(n, w, h int, c Config, minW, minH int) (int, float64, bool) {
	bestRows, bestScore, ok := 1, -1.0, false
	for r := 1; r <= n; r++ {
		cols := (n + r - 1) / r
		cellW := (w - (cols - 1)) / cols
		cellH := (h - (r - 1)) / r
		if cellW < minW || cellH < minH {
			continue
		}
		s := score(cellW, cellH, c)
		if n%r == 0 {
			s *= evenBonus
		}
		if s > bestScore {
			bestRows, bestScore, ok = r, s, true
		}
	}
	return bestRows, bestScore, ok
}

// score rates one cell size against the minimum pane size, clamped at 1.
func score(cellW, cellH int, c Config) float64 {
	sw := float64(cellW) / float64(c.MinPaneW)
	sh := float64(cellH) / float64(c.MinPaneH)
	s := sw
	if sh < s {
		s = sh
	}
	if s > 1 {
		s = 1
	}
	return s
}

// spread distributes n panes over r rows, remainder on the bottom rows, so new
// panes land at the end and the top rows keep their shape: 5/2 -> [2 3].
func spread(n, r int) []int {
	out := make([]int, r)
	base, rem := n/r, n%r
	for i := range out {
		out[i] = base
		if i >= r-rem {
			out[i]++
		}
	}
	return out
}
