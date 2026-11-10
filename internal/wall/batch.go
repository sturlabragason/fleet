package wall

import (
	"slices"
	"strconv"
	"strings"

	"github.com/sturlabragason/fleet/internal/layout"
	"github.com/sturlabragason/fleet/internal/logx"
)

// Batched moves (DESIGN.md ADR-88). Moving panes on or off the wall one tmux
// call at a time resizes the window's panes once per move: every app redraws
// at a throwaway size, then tmux holds the next resize back 250 ms. A
// five-pane restore left the overview and agent panes blank or torn for
// seconds while the TUIs redrew twice. A batch sends the moves, the reorder
// and the final layout as one tmux command list, so tmux tells each pane its
// size once, the final one, as soon as the list has run.

// plan is a rendered layout for a state.
type plan struct {
	lay     string
	leaves  []string
	rows    []int
	hide    []string // admirals that do not fit the column (DESIGN §6.12)
	adopted bool
}

// plan renders the layout for s. Admirals that do not fit are dropped from
// s and listed in hide, for the caller to move out of the window.
func (w *Wall) plan(s *State) plan {
	var p plan
	if fit := s.fitSups(); len(fit) < len(s.Sups) {
		p.hide = slices.Clone(s.Sups[len(fit):])
		s.Sups, s.SupNames, s.SupRows = fit, s.SupNames[:len(fit)], s.SupRows[:len(fit)]
		s.Panes = nil // the pane set changed: no drag to adopt
	}
	var ids []string
	for _, wp := range s.Wall {
		ids = append(ids, wp.ID)
	}
	w.syncNextOut(s)
	var want []int
	want, p.adopted = w.supRows(s)
	p.rows = s.Cfg.SupRowsFor(want, len(s.Sups), s.H)
	p.lay, p.leaves = s.Cfg.Render(layout.Input{W: s.W, H: s.H, Sups: s.Sups, SupRows: p.rows, Ov: s.Ov, Wall: ids, Preset: s.Preset, Focus: w.emphasis(s)})
	return p
}

// remember records an applied plan.
func (w *Wall) remember(s *State, p plan) {
	w.setOpt("@fleet_layout", p.lay)
	if p.rows != nil {
		w.storeSupRows(s.Sups, p.rows)
	}
	w.setOpt(supSeenOption, seenKey(s.H, p.leaves))
}

// batch collects pane moves on the main window for one tmux call.
type batch struct {
	w     *Wall
	args  []string
	order []string // the window's panes as they will be after the moves
}

func (w *Wall) newBatch(s *State) *batch {
	return &batch{w: w, order: slices.Clone(s.Panes)}
}

func (b *batch) add(args ...string) {
	if len(b.args) > 0 {
		b.args = append(b.args, ";")
	}
	b.args = append(b.args, args...)
}

// join puts pane on the wall after the pane `after`. The tiled spread keeps
// room for the next join; the panes only ever see the final layout.
func (b *batch) join(pane, after string) {
	b.add("join-pane", "-d", "-s", pane, "-t", after)
	b.add("set-option", "-p", "-t", pane, "@role", "wall")
	b.add("select-layout", "-t", b.w.Target, "tiled")
	b.order = slices.Insert(b.order, index(b.order, after)+1, pane)
}

// breakOut takes pane off the wall into its own window.
func (b *batch) breakOut(pane string) {
	b.breakTo(pane, b.w.agentWindow(pane))
	b.add("set-option", "-p", "-t", pane, "@role", "agent")
}

func (b *batch) breakTo(pane, win string) {
	session := strings.SplitN(b.w.Target, ":", 2)[0]
	b.add("break-pane", "-d", "-s", pane, "-t", session+":", "-n", win)
	if i := index(b.order, pane); i >= 0 {
		b.order = slices.Delete(b.order, i, i+1)
	}
}

// commit runs the moves and lays the window out for s, the state they leave
// behind, in one tmux call. It reports whether that call ran; when it did
// not (a move failed, or another process holds the apply lock), the moves
// that ran are laid out by a plain Apply.
func (b *batch) commit(s *State) (bool, error) {
	w := b.w
	log := logx.For("wall")
	// Shedding panes over the floor is apply's job.
	if len(s.Wall) > s.MaxPanes() || !w.lock() {
		if len(b.args) > 0 {
			if _, err := w.T.Run(b.args...); err != nil {
				log.Warn("batch", "err", err)
			}
		}
		return false, w.Apply()
	}
	s.Panes = nil // the pane set changes: no drag to adopt
	p := w.plan(s)
	for _, id := range p.hide {
		b.breakTo(id, "sup-hidden")
	}
	active := ""
	sw := swaps(slices.Clone(b.order), p.leaves)
	if len(sw) > 0 {
		active, _ = w.T.Run("display-message", "-p", "-t", w.Target, "#{pane_id}")
		active = strings.TrimSpace(active)
	}
	moved := false
	for _, x := range sw {
		b.add("swap-pane", "-d", "-s", x[0], "-t", x[1])
		moved = moved || x[0] == active || x[1] == active
	}
	b.add("select-layout", "-t", w.Target, p.lay)
	if len(s.Sups) > 0 {
		b.add("resize-pane", "-t", s.Sups[0], "-x", strconv.Itoa(s.Cfg.SupCols(s.W)))
	}
	// A chain of swap-pane -d can move the focus (see reorder); selecting
	// the pane that already has it is a no-op for tmux.
	if moved && active != "" {
		b.add("select-pane", "-t", active)
	}
	_, err := w.T.Run(b.args...)
	if err == nil {
		w.remember(s, p)
	}
	w.unlock()
	if err != nil {
		log.Warn("batch", "err", err)
		return false, w.Apply()
	}
	return true, nil
}
