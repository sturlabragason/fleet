package layout

// SupRowsFor fits wanted admiral heights to a column h rows high holding n
// stacked admirals (n-1 border rows between them): want as it is when it
// fits, equal rows when want does not describe n panes, and nil when the
// column has no room for n panes.
// In a column of another height than want was made for, a pane shorter than
// SupMinRows (minimized) keeps its rows and the others scale proportionally
// (ADR-84).
func (c Config) SupRowsFor(want []int, n, h int) []int {
	avail := h - (n - 1)
	if n < 2 || avail < n {
		return nil
	}
	if len(want) != n {
		return split(h, n)
	}
	sum, fixed, scaled := 0, 0, 0
	for _, r := range want {
		if r < 1 {
			return split(h, n)
		}
		sum += r
		if r < c.SupMinRows {
			fixed += r
		} else {
			scaled += r
		}
	}
	out := make([]int, n)
	if sum == avail {
		copy(out, want)
		return out
	}
	if scaled == 0 || avail-fixed < 1 { // nothing to scale: scale everything
		fixed, scaled = 0, sum
	}
	got, big := 0, 0
	for i, r := range want {
		out[i] = r
		if fixed == 0 || r >= c.SupMinRows {
			out[i] = max(1, r*(avail-fixed)/scaled)
		}
		got += out[i]
		if out[i] > out[big] {
			big = i
		}
	}
	// The rounding remainder (or the overshoot from the 1-row floor) goes to
	// the largest pane.
	out[big] += avail - got
	if out[big] < 1 {
		return nil
	}
	return out
}
