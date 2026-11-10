package wall

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Admiral rows (ADR-84). Stacked admirals keep the heights the user gave
// them through every relayout. @fleet_sup_rows holds the rows per admiral
// pane id ("%1=76 %5=3"), kept while the admirals are hidden; @fleet_sup_seen
// holds the window height and pane set of the last apply. When an apply finds
// the same window and pane set but other admiral heights, nothing but the user
// (a border drag or resize-pane) changed them, and they are adopted. A
// join-pane, break-pane or window resize changes the key, so the stored rows
// win and are refitted to the column.

// MinimizedRows is the height of a minimized admiral.
const MinimizedRows = 2

const (
	supRowsOption = "@fleet_sup_rows"
	supSeenOption = "@fleet_sup_seen"
)

// seenKey identifies a window shape: its height and its sorted pane ids.
func seenKey(h int, panes []string) string {
	ids := slices.Clone(panes)
	slices.Sort(ids)
	return strconv.Itoa(h) + " " + strings.Join(ids, " ")
}

func (w *Wall) storedSupRows() map[string]int {
	out := map[string]int{}
	for _, f := range strings.Fields(w.opt(supRowsOption)) {
		id, n, ok := strings.Cut(f, "=")
		if r, err := strconv.Atoi(n); ok && err == nil && r > 0 {
			out[id] = r
		}
	}
	return out
}

// storeSupRows records the rows of the given admirals, keeping the entries of
// admirals that are not in the window (hidden ones) as they were.
func (w *Wall) storeSupRows(sups []string, rows []int) {
	m := w.storedSupRows()
	for i, id := range sups {
		if i < len(rows) {
			m[id] = rows[i]
		}
	}
	var ids []string
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var b []string
	for _, id := range ids {
		b = append(b, fmt.Sprintf("%s=%d", id, m[id]))
	}
	w.setOpt(supRowsOption, strings.Join(b, " "))
}

// supRows returns the admiral heights the next layout should keep, and
// whether they are the user's drag adopted from the window.
func (w *Wall) supRows(s *State) ([]int, bool) {
	if len(s.Sups) < 2 {
		return nil, false
	}
	stored := w.storedSupRows()
	want := make([]int, len(s.Sups))
	known := 0
	for i, id := range s.Sups {
		if r := stored[id]; r > 0 {
			want[i] = r
			known++
		}
	}
	if known == len(want) && len(s.SupRows) == len(want) && !slices.Equal(want, s.SupRows) &&
		w.opt(supSeenOption) == seenKey(s.H, s.Panes) {
		return slices.Clone(s.SupRows), true
	}
	if known == 0 {
		return nil, false
	}
	if known < len(want) { // a new admiral: an even share, the rest keep theirs
		avail := s.H - (len(want) - 1)
		share := avail / len(want)
		for i := range want {
			if want[i] == 0 {
				want[i] = share
			}
		}
	}
	return want, false
}

// MinimizeAdmiral toggles the named admiral ("main" for the main one) between
// MinimizedRows and an even share of the admiral column; the others fill the
// rest in their current proportions. It returns true when the admiral is now
// minimized.
func (w *Wall) MinimizeAdmiral(name string) (bool, error) {
	s, err := w.Read()
	if err != nil {
		return false, err
	}
	i := slices.Index(s.SupNames, name)
	if i < 0 {
		return false, fmt.Errorf("admiral %q is not in the main window", name)
	}
	n := len(s.Sups)
	if n < 2 {
		return false, fmt.Errorf("only one admiral is shown; there is nothing to make room for")
	}
	avail := s.H - (n - 1)
	cur := s.SupRows
	mine, minimized := MinimizedRows, true
	if cur[i] <= MinimizedRows {
		mine, minimized = avail/n, false
	}
	others := slices.Delete(slices.Clone(cur), i, i+1)
	var rest []int
	if len(others) == 1 {
		rest = []int{avail - mine}
	} else {
		rest = s.Cfg.SupRowsFor(others, len(others), avail-mine+len(others)-1)
	}
	if rest == nil {
		return false, fmt.Errorf("the admiral column is too short")
	}
	rows := slices.Insert(rest, i, mine)
	w.storeSupRows(s.Sups, rows)
	w.setOpt(supSeenOption, "")
	return minimized, w.Apply()
}
