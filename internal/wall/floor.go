package wall

import (
	"fmt"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// The readability floor on the wall (DESIGN.md §5b.4, ADR-41). The floor
// itself is geometry and lives in internal/layout; this is the part that
// decides how many panes the wall may hold because of it.

// MaxPanes is how many panes this window can show above the floor, in the
// shape the current preset renders.
func (s *State) MaxPanes() int {
	w, h := s.Cfg.WallRect(s.W, s.H, len(s.Sups) > 0)
	return s.Cfg.MaxPanes(s.Preset, w, h)
}

// Slots is the preset's slot cap, stepped down to what fits above the floor.
// The preset itself is never rewritten: growing the window takes the cap back
// up on the next apply.
func (s *State) Slots() int {
	w, h := s.Cfg.WallRect(s.W, s.H, len(s.Sups) > 0)
	return s.Cfg.FitSlots(s.Preset, w, h)
}

// Capped reports whether the floor, not the preset, is what limits the wall.
func (s *State) Capped() bool { return s.Slots() < s.Cfg.Slots(s.Preset) }

// FullMsg is shown when every wall pane is protected from eviction.
const FullMsg = "wall full: every pane is waiting or failing (M-x to free one)"

// FloorFullMsg is shown when the window, not the preset, is what is full.
const FloorFullMsg = "wall full: another pane would be too small to read here (M-x to free one, or grow the window)"

func steppedMsg(preset string, slots int) string {
	return fmt.Sprintf("preset %s: this window fits %d readable panes", preset, slots)
}

// enforceFloor drops wall panes that no longer fit above the floor, in the
// usual class + least-recently-used order. Protected panes stay: ADR-14 still
// says a pinned, waiting or failing pane is never taken away, so a wall of
// nothing but those goes cramped rather than hiding one. The ▶ target pane is
// exempt too, because it is the pane the user just chose (ADR-31): an
// explicit choice is never undone by the floor on the apply that follows it.
// Called from every apply, which is what the client-resized hook runs, so
// shrinking the terminal sheds panes instead of crushing them.
func (w *Wall) enforceFloor(s *State) int {
	max := s.MaxPanes()
	if len(s.Wall) <= max {
		return 0
	}
	target := w.opt("@fleet_target")
	dropped := 0
	for len(s.Wall) > max {
		id, ok := EvictCandidate(evictable(s.Wall, target))
		if !ok {
			break
		}
		w.breakOut(id)
		s.Wall = remove(s.Wall, id)
		dropped++
	}
	if dropped == 0 {
		return 0
	}
	logx.For("wall").Info("under the readable floor", "dropped", dropped, "max", max, "window", fmt.Sprintf("%dx%d", s.W, s.H))
	w.T.Run("display-message", "-t", w.Target, fmt.Sprintf("window too small: showing %d pane(s) so they stay readable", len(s.Wall)))
	return dropped
}

// evictable is the wall without the target pane.
func evictable(ps []tmux.PaneInfo, target string) []tmux.PaneInfo {
	if target == "" {
		return ps
	}
	return remove(ps, target)
}

// fitSups is the admirals that fit one column of this window: main first, the
// rest while each keeps its minimum rows (DESIGN §6.12).
func (s *State) fitSups() []string {
	if len(s.Sups) == 0 {
		return nil
	}
	return s.Sups[:s.Cfg.SupFit(s.H, len(s.Sups))]
}

func (s *State) firstSup() string {
	if len(s.Sups) == 0 {
		return ""
	}
	return s.Sups[0]
}
