package wall

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// The target slot (DESIGN.md §5e, ADR-31): the window option @fleet_target
// holds the pane id of the wall pane the next card goes into. The
// pane-focus-in hook sets it when a wall pane gets focus; Show, Add and Next
// set it to the pane they bring on. A pane that is no longer on the wall
// counts as no target.

// AddFullMsg is shown when Add is asked for a tenth pane.
const AddFullMsg = "wall full (9) · click a card to replace ▶"

// NothingWaitingMsg is shown when Next finds no waiting agent.
const NothingWaitingMsg = "nothing waiting"

// presetUp is the next bigger preset for Add on a full wall.
var presetUp = map[string]string{"1": "2", "2": "4", "4": "6", "m": "6", "6": "9"}

func onWall(s *State, pane string) bool {
	_, ok := targetPane(s, pane)
	return ok
}

// targetPane returns the wall pane with id, if it is on the wall.
func targetPane(s *State, id string) (tmux.PaneInfo, bool) {
	if id == "" {
		return tmux.PaneInfo{}, false
	}
	return paneByID(s.Wall, id)
}

// TargetAgent is the agent in the target slot, "" when there is no valid target.
func (w *Wall) TargetAgent() string {
	s, err := w.Read()
	if err != nil {
		return ""
	}
	p, _ := targetPane(s, w.opt("@fleet_target"))
	return p.Agent
}

// Untarget clears the target slot (`fleet wall untarget`, Esc in the overview).
func (w *Wall) Untarget() error {
	w.setOpt("@fleet_target", "")
	return w.ApplyCached() // the "next out" mark may come back
}

// Add appends a pane as a new wall slot without replacing anything. On a full
// wall the preset steps up first (1→2→4→6→9, m→6); at 9, or once another
// pane would fall under the readability floor (ADR-41), it refuses with a
// message and returns false. A pane already on the wall is focused.
func (w *Wall) Add(pane string) (bool, error) {
	s, err := w.Read()
	if err != nil {
		return false, err
	}
	now := strconv.FormatInt(time.Now().UnixNano(), 10)
	if onWall(s, pane) {
		return w.focus(pane, now, !w.KeepFocus)
	}
	for len(s.Wall) >= s.Slots() {
		up, ok := presetUp[s.Preset]
		if !ok {
			w.T.Run("display-message", "-t", w.Target, AddFullMsg)
			logx.For("wall").Info("add refused, wall at 9", "pane", pane)
			return false, nil
		}
		prev := s.Preset
		s.Preset = up
		if s.Capped() && s.Slots() <= len(s.Wall) {
			// A bigger preset cannot help: the window is what is full, not
			// the preset (ADR-41). Don't join the pane, and say why.
			s.Preset = prev
			w.T.Run("display-message", "-t", w.Target, FloorFullMsg)
			logx.For("wall").Info("add refused, below the readable floor", "pane", pane, "max", s.MaxPanes())
			return false, nil
		}
	}
	if s.Preset != w.opt("@fleet_wall") {
		w.setOpt("@fleet_wall", s.Preset)
	}
	return w.join(s, pane, now, !w.KeepFocus)
}

// Next shows the waiting agent after (dir=+1) or before (dir=-1) the one in
// the target slot, longest wait first and wrapping; the first one when the
// target holds none of them. It returns the agent name, "" when nothing is
// waiting (a tmux message says so), and ok=false when the wall refused it
// (full, nothing evictable: FullMsg). Next never moves keyboard focus, from
// the overview or from Alt-n in a pane, so Tab can be pressed again.
func (w *Wall) Next(dir int) (name string, ok bool, err error) {
	s, err := w.Read()
	if err != nil {
		return "", false, err
	}
	all, err := w.T.ListPanes("")
	if err != nil {
		return "", false, err
	}
	waiting := waitingPanes(all, strings.SplitN(w.Target, ":", 2)[0])
	if len(waiting) == 0 {
		w.T.Run("display-message", "-t", w.Target, NothingWaitingMsg)
		return "", true, nil
	}
	pick := waiting[0]
	if t, ok := targetPane(s, w.opt("@fleet_target")); ok {
		for i, p := range waiting {
			if p.ID == t.ID {
				n := len(waiting)
				pick = waiting[((i+dir)%n+n)%n]
			}
		}
	}
	ok, err = w.show(pick.ID, true, false)
	return pick.Agent, ok, err
}

// waitingPanes are the session's live agent panes that wait for me, longest
// wait first (an untagged @fleet_status_since sorts last).
func waitingPanes(all []tmux.PaneInfo, session string) []tmux.PaneInfo {
	var out []tmux.PaneInfo
	for _, p := range all {
		if p.Session == session && p.Agent != "" && !p.Dead && p.Status == "waiting" {
			out = append(out, p)
		}
	}
	since := func(p tmux.PaneInfo) int64 {
		if p.StatusSince == 0 {
			return math.MaxInt64
		}
		return p.StatusSince
	}
	sort.SliceStable(out, func(i, j int) bool { return since(out[i]) < since(out[j]) })
	return out
}

// syncNextOut keeps @fleet_next_out on the pane a card would evict: set only
// when there is no valid target and the wall is full. It costs one tmux read
// and writes only when the value changes.
func (w *Wall) syncNextOut(s *State) {
	out, _ := w.T.Run("display-message", "-p", "-t", w.Target, "#{@fleet_target}\t#{@fleet_next_out}")
	target, cur, _ := strings.Cut(strings.TrimSpace(out), "\t")
	want := ""
	if _, ok := targetPane(s, target); !ok && len(s.Wall) >= s.Slots() {
		want, _ = EvictCandidate(s.Wall)
	}
	if want != cur {
		w.setOpt("@fleet_next_out", want)
	}
}
