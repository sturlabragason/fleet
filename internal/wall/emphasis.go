package wall

import (
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/sturlabragason/fleet/internal/tmux"
)

// Hysteresis for the emphasis pane (DESIGN.md §5b.3, ADR-16 as amended by
// ADR-31): resizing a pane makes an agent TUI reflow, so emphasis moves
// reluctantly. It follows attention only; focusing a pane never picks it.
const (
	EmphasisDwell = 10 * time.Second // a candidate must have waited this long
	EmphasisHold  = 30 * time.Second // ... and the current choice held this long
)

// chooseEmphasis picks the emphasis pane: the wall pane that has needed me the
// longest (waiting before error/stuck), or "" for an even grid. cur/at are the
// cached choice and when it was made. It is pure so the hysteresis can be
// tested without tmux.
func chooseEmphasis(ps []tmux.PaneInfo, cur string, at, now time.Time) string {
	best := bestCandidate(ps)
	fallback := func() string {
		if best != nil {
			return best.ID
		}
		return ""
	}
	if cur == "" {
		return fallback()
	}
	curPane, ok := paneByID(ps, cur)
	if !ok || !needsAttention(curPane.Status) {
		return fallback() // it left the wall or stopped needing attention
	}
	if best == nil || best.ID == cur {
		return cur
	}
	// StatusSince 0 means "untagged by an older build": the dwell is unknown,
	// so it is not satisfied and the pane cannot steal emphasis yet.
	if best.StatusSince == 0 {
		return cur
	}
	waited := now.Sub(time.Unix(0, best.StatusSince))
	if now.Sub(at) >= EmphasisHold && waited >= EmphasisDwell {
		return best.ID
	}
	return cur
}

// bestCandidate is the pane that most needs attention: waiting before
// error/stuck, oldest @fleet_status_since first. nil when nothing needs me.
func bestCandidate(ps []tmux.PaneInfo) *tmux.PaneInfo {
	var c []tmux.PaneInfo
	for _, p := range ps {
		if needsAttention(p.Status) {
			c = append(c, p)
		}
	}
	if len(c) == 0 {
		return nil
	}
	rank := func(p tmux.PaneInfo) int {
		if p.Status == "waiting" {
			return 0
		}
		return 1
	}
	// StatusSince 0 means "untagged by an older build": the wait is unknown, so
	// it sorts last. Sorting it first would let one untagged pane sit at the
	// head of the queue and (via the dwell guard) block emphasis forever.
	since := func(p tmux.PaneInfo) int64 {
		if p.StatusSince == 0 {
			return math.MaxInt64
		}
		return p.StatusSince
	}
	sort.SliceStable(c, func(i, j int) bool {
		if ri, rj := rank(c[i]), rank(c[j]); ri != rj {
			return ri < rj
		}
		return since(c[i]) < since(c[j])
	})
	return &c[0]
}

func paneByID(ps []tmux.PaneInfo, id string) (tmux.PaneInfo, bool) {
	for _, p := range ps {
		if p.ID == id {
			return p, true
		}
	}
	return tmux.PaneInfo{}, false
}

// emphasis returns the pane to emphasise for this state, updating the cached
// choice in window options. "" means an even grid.
func (w *Wall) emphasis(s *State) string {
	if !w.emphasisOn() || len(s.Wall) < 2 {
		return ""
	}
	cur := w.opt("@fleet_emphasis_pane")
	at, _ := strconv.ParseInt(w.opt("@fleet_emphasis_at"), 10, 64)
	now := w.now()
	pick := chooseEmphasis(s.Wall, cur, time.Unix(0, at), now)
	if pick != cur {
		w.setOpt("@fleet_emphasis_pane", pick)
		w.setOpt("@fleet_emphasis_at", strconv.FormatInt(now.UnixNano(), 10))
	}
	return pick
}

// emphasisOn reports whether emphasis is enabled. It is off unless
// `fleet wall emphasis` turned it on: a pane growing on its own when an agent
// starts waiting moves everything else under the user's pointer (§5e).
func (w *Wall) emphasisOn() bool { return w.opt("@fleet_emphasis") == "1" }

// ToggleEmphasis switches the emphasis pane on and back off (`fleet wall emphasis`).
func (w *Wall) ToggleEmphasis() error {
	v := "1"
	if w.emphasisOn() {
		v = "0"
	}
	w.setOpt("@fleet_emphasis", v)
	return w.Apply()
}

func (w *Wall) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}
