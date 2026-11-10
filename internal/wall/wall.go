// Package wall manages the main fleet window: admiral, overview and the
// live wall of agent panes moved in with join-pane / out with break-pane.
// All state lives in tmux (window options and pane @role/@fleet_used tags),
// so the TUI and `fleet wall ...` key bindings can both drive it.
package wall

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/layout"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// Wall drives one main window.
type Wall struct {
	T      *tmux.Client
	Target string // e.g. "fleet:main"
	Cfg    layout.Config
	Now    func() time.Time // clock for the emphasis hysteresis (default time.Now)
	// KeepFocus makes Show, Place and Add leave keyboard focus where it is
	// (the overview sets it, so a second key press still reaches the overview,
	// not the agent it just put on the wall). @fleet_target is set explicitly.
	KeepFocus bool
	// Snap, when set, answers the read-only hot path (Read, OnWall,
	// Pinned, TargetAgent, ApplyCached) from the watcher's shared snapshot
	// instead of tmux (ADR-98). Only copies made by WithSnapshot set it.
	Snap  *tmux.Snapshot
	wrote *bool // set when a snapshot-backed plan wanted to write
}

// WithSnapshot returns a copy of w that reads s instead of tmux. A cached
// apply on it goes to tmux only when the snapshot shows something to do.
func (w *Wall) WithSnapshot(s *tmux.Snapshot) *Wall {
	c := *w
	c.Snap, c.wrote = s, nil
	return &c
}

// live is w reading tmux.
func (w *Wall) live() *Wall {
	c := *w
	c.Snap, c.wrote = nil, nil
	return &c
}

// snapWindow is the main window's fields in the snapshot.
func (w *Wall) snapWindow() (map[string]string, bool) {
	if w.Snap == nil {
		return nil, false
	}
	return w.Snap.Window(w.Target)
}

// listPanes is ListPanes, answered by the snapshot when there is one.
func (w *Wall) listPanes(target string) ([]tmux.PaneInfo, error) {
	if w.Snap != nil {
		return w.Snap.PanesOf(target), nil
	}
	return w.T.ListPanes(target)
}

// New returns a wall for session:main.
func New(t *tmux.Client, session string) *Wall {
	return &Wall{T: t, Target: session + ":main", Cfg: layout.Default}
}

// State is the parsed main window.
type State struct {
	W, H      int
	Sups      []string // admiral panes, main first
	SupNames  []string // admiral names, parallel to Sups ("main" for the main one)
	SupRows   []int    // admiral heights now, parallel to Sups
	Panes     []string // every pane id in the window
	Ov        string
	Wall      []tmux.PaneInfo // in window order
	Preset    string
	Cfg       layout.Config
	SupHidden string // pane id of hidden admiral, if any
}

func (w *Wall) opt(name string) string {
	if win, ok := w.snapWindow(); ok {
		if v, ok := win[name]; ok {
			return v
		}
	}
	s, _ := w.T.Run("show-options", "-wqv", "-t", w.Target, name)
	return strings.TrimSpace(s)
}

func (w *Wall) setOpt(name, val string) {
	if w.wrote != nil {
		*w.wrote = true // a snapshot-backed plan: the live apply writes it
		return
	}
	w.T.Run("set-option", "-w", "-t", w.Target, name, val)
}

// Read loads the current state.
func (w *Wall) Read() (*State, error) {
	var dims string
	if w.Snap != nil {
		win, ok := w.snapWindow()
		if !ok {
			return nil, fmt.Errorf("no window %s in the snapshot", w.Target)
		}
		dims = win["window_width"] + " " + win["window_height"]
	} else {
		var err error
		if dims, err = w.T.Run("display-message", "-p", "-t", w.Target, "#{window_width} #{window_height}"); err != nil {
			return nil, err
		}
	}
	s := &State{Cfg: w.Cfg, Preset: w.opt("@fleet_wall")}
	fmt.Sscan(dims, &s.W, &s.H)
	if s.Preset == "" {
		s.Preset = "4"
	}
	if v := w.opt("@fleet_sup_side"); v != "" {
		s.Cfg.SupSide = v
	}
	s.Cfg.ChipsOnly = w.opt("@fleet_chips") == "1"
	panes, err := w.listPanes(w.Target)
	if err != nil {
		return nil, err
	}
	for _, p := range panes {
		s.Panes = append(s.Panes, p.ID)
		switch p.Role {
		case "sup":
			if p.Admiral == "" || p.Admiral == "main" {
				s.Sups = append([]string{p.ID}, s.Sups...)
				s.SupNames = append([]string{"main"}, s.SupNames...)
				s.SupRows = append([]int{p.Height}, s.SupRows...)
			} else {
				s.Sups = append(s.Sups, p.ID)
				s.SupNames = append(s.SupNames, p.Admiral)
				s.SupRows = append(s.SupRows, p.Height)
			}
		case "ov":
			s.Ov = p.ID
		default:
			s.Wall = append(s.Wall, p)
		}
	}
	return s, nil
}

// Apply recomputes and applies the full layout (never `tiled`), ignoring the
// `@fleet_layout` cache. Explicit `fleet wall ...` commands and the tmux hooks
// use this: `mouse on` lets the user drag borders and tmux clamps sizes after
// select-layout, so the real layout drifts from the cached string and a cached
// apply would wedge the wall in the drifted shape forever.
func (w *Wall) Apply() error { return w.apply(true) }

// ApplyCached is Apply for the hot path (the TUI refresh loop): it skips the
// whole tmux round trip when the rendered layout equals `@fleet_layout`.
func (w *Wall) ApplyCached() error { return w.apply(false) }

func (w *Wall) apply(force bool) error {
	if w.Snap != nil {
		if !force && w.snapUnchanged() {
			return nil
		}
		return w.live().apply(force)
	}
	s, err := w.Read()
	if err != nil {
		return err
	}
	// break-pane mutates the window, so shedding panes takes the apply lock
	// too. The state is reread under the lock -- a pane may have been pinned
	// or started waiting since the first read, and evicting on that stale
	// snapshot would take away a protected pane -- and again afterwards,
	// because the pane list changed.
	if len(s.Wall) > s.MaxPanes() {
		if !w.lock() {
			return nil
		}
		if fresh, err := w.Read(); err == nil {
			s = fresh
		}
		dropped := w.enforceFloor(s)
		w.unlock()
		if dropped > 0 {
			if fresh, err := w.Read(); err == nil {
				s = fresh
			}
		}
	}
	p := w.plan(s)
	session := strings.SplitN(w.Target, ":", 2)[0]
	for _, id := range p.hide {
		w.T.Run("break-pane", "-d", "-s", id, "-t", session+":", "-n", "sup-hidden")
	}
	lay, leaves, adopted := p.lay, p.leaves, p.adopted
	remember := func() { w.remember(s, p) }
	// An unchanged layout is never re-applied: agent TUIs reflow on every resize.
	if !force && lay == w.opt("@fleet_layout") {
		return nil
	}
	// The refresh loop adopting a drag the window already shows has nothing
	// to apply.
	if !force && adopted && lay == w.windowLayout() {
		remember()
		return nil
	}
	// The TUI poll loop, the tmux hooks and `fleet wall ...` are separate
	// processes: two reorders from independent snapshots would scramble pane
	// order. Whoever holds the lock wins; the loser's state is stale anyway.
	if !w.lock() {
		return nil
	}
	defer w.unlock()
	w.reorder(leaves)
	if _, err := w.T.Run("select-layout", "-t", w.Target, lay); err != nil {
		logx.For("wall").Error("select-layout", "layout", lay, "err", err)
		return err
	}
	remember()
	if len(s.Sups) > 0 {
		w.T.Run("resize-pane", "-t", s.Sups[0], "-x", strconv.Itoa(s.Cfg.SupCols(s.W)))
	}
	return nil
}

// snapUnchanged reports whether the snapshot proves a cached apply has
// nothing to do: no pane to shed or hide, no option to write, and the plan's
// layout is the one last applied. Anything else is left to a live apply,
// which reads tmux again before it changes a thing (ADR-98).
func (w *Wall) snapUnchanged() bool {
	s, err := w.Read()
	if err != nil || len(s.Wall) > s.MaxPanes() {
		return false
	}
	c := *w
	wrote := false
	c.wrote = &wrote
	p := c.plan(s)
	return !wrote && len(p.hide) == 0 && p.lay == c.opt("@fleet_layout")
}

// lock takes the apply lock, a window option holding the owner's deadline. It
// returns false when another process holds it. A stale lock (an apply that
// crashed) expires after applyLockTTL.
func (w *Wall) lock() bool {
	now := w.now()
	if v := w.opt("@fleet_applying"); v != "" {
		if until, err := strconv.ParseInt(v, 10, 64); err == nil && now.UnixNano() < until {
			return false
		}
	}
	w.setOpt("@fleet_applying", strconv.FormatInt(now.Add(applyLockTTL).UnixNano(), 10))
	return true
}

func (w *Wall) unlock() { w.setOpt("@fleet_applying", "") }

// windowLayout is the window's current tmux layout string.
func (w *Wall) windowLayout() string {
	if win, ok := w.snapWindow(); ok {
		return win["window_layout"]
	}
	s, _ := w.T.Run("display-message", "-p", "-t", w.Target, "#{window_layout}")
	return strings.TrimSpace(s)
}

// applyLockTTL bounds how long a crashed apply can block the next one.
const applyLockTTL = 5 * time.Second

// reorder swaps panes so window order matches layout leaf order. A chain of
// `swap-pane -d` can leave the focus on a different pane, so the focus is
// restored. The restore is deliberately narrow — it only fires when the focus
// both started and ended on panes this call swapped — because a select-pane
// that fought a concurrent focus change would move the user (and, through the
// pane-focus-in hook, the target slot).
func (w *Wall) reorder(want []string) {
	before, _ := w.T.Run("display-message", "-p", "-t", w.Target, "#{pane_id}")
	before = strings.TrimSpace(before)
	swapped := map[string]bool{}
	defer func() {
		after, _ := w.T.Run("display-message", "-p", "-t", w.Target, "#{pane_id}")
		after = strings.TrimSpace(after)
		if before == "" || after == before || !swapped[before] || !swapped[after] {
			return
		}
		w.T.Run("select-pane", "-t", before)
	}()
	panes, _ := w.T.ListPanes(w.Target)
	cur := make([]string, len(panes))
	for i, p := range panes {
		cur[i] = p.ID
	}
	for _, sw := range swaps(cur, want) {
		w.T.Run("swap-pane", "-d", "-s", sw[0], "-t", sw[1])
		swapped[sw[0]], swapped[sw[1]] = true, true
	}
}

// swaps lists the `swap-pane -s A -t B` pairs that put the panes in cur into
// want's order. cur is reordered in place.
func swaps(cur, want []string) [][2]string {
	var out [][2]string
	for i, id := range want {
		if i >= len(cur) || cur[i] == id {
			continue
		}
		j := index(cur, id)
		if j < 0 {
			continue
		}
		out = append(out, [2]string{cur[i], id})
		cur[i], cur[j] = cur[j], cur[i]
	}
	return out
}

func index(l []string, s string) int {
	for i, v := range l {
		if v == s {
			return i
		}
	}
	return -1
}

// Show brings an agent pane onto the wall (DESIGN.md §5e). A pane already
// there is focused. Otherwise it goes into the target slot (@fleet_target)
// when there is one that is not pinned, full wall or not; without a target it
// is appended if there is room, else the least needed pane is evicted (§5b.1).
// It reports whether the pane ended up on the wall: false with a nil error is
// the normal "wall full, nothing evictable" refusal.
func (w *Wall) Show(pane string) (bool, error) { return w.show(pane, true, !w.KeepFocus) }

// Place is Show without the target slot: append if there is room, else evict
// by class. `fleet team show` uses it, so members fill the wall instead of
// replacing each other in the ▶ slot.
func (w *Wall) Place(pane string) (bool, error) { return w.show(pane, false, !w.KeepFocus) }

// show is Show/Place; selectPane=false leaves keyboard focus where it is.
func (w *Wall) show(pane string, useTarget, selectPane bool) (bool, error) {
	log := logx.For("wall")
	s, err := w.Read()
	if err != nil {
		return false, err
	}
	now := strconv.FormatInt(time.Now().UnixNano(), 10)
	if onWall(s, pane) {
		return w.focus(pane, now, selectPane)
	}
	if t, ok := targetPane(s, w.opt("@fleet_target")); useTarget && ok && !t.Pin {
		w.setOpt("@fleet_undo", undoRecord(pane, t.ID, t.Agent))
		return w.swapInto(pane, t.ID, now)
	}
	slots := s.Slots()
	for len(s.Wall) >= slots {
		id, ok := EvictCandidate(s.Wall)
		if !ok {
			// Everything on the wall needs me: leave it alone and say so.
			msg := FullMsg
			if s.Capped() {
				msg = FloorFullMsg
			}
			w.T.Run("display-message", "-t", w.Target, msg)
			log.Info("wall full, nothing evictable", "pane", pane, "slots", slots, "capped", s.Capped())
			return false, nil
		}
		w.breakOut(id)
		s.Wall = remove(s.Wall, id)
	}
	return w.join(s, pane, now, selectPane)
}

// focus makes a pane already on the wall the target and, with selectPane,
// selects it.
func (w *Wall) focus(pane, now string, selectPane bool) (bool, error) {
	w.T.SetPaneOption(pane, "@fleet_used", now)
	w.setOpt("@fleet_target", pane)
	if !selectPane {
		return true, nil
	}
	_, err := w.T.Run("select-pane", "-t", pane)
	return true, err
}

// join appends a pane as the last wall slot, re-tiles and makes it the
// target; with selectPane it is also focused.
func (w *Wall) join(s *State, pane, now string, selectPane bool) (bool, error) {
	target := s.Ov
	if len(s.Wall) > 0 {
		target = s.Wall[len(s.Wall)-1].ID
	}
	if _, err := w.T.Run("join-pane", "-d", "-s", pane, "-t", target); err != nil {
		if _, err2 := w.T.Run("join-pane", "-d", "-s", pane, "-t", s.firstSup()); err2 != nil {
			return false, err
		}
	}
	w.T.SetPaneOption(pane, "@role", "wall")
	w.T.SetPaneOption(pane, "@fleet_used", now)
	w.setOpt("@fleet_target", pane)
	logx.For("wall").Info("on wall", "pane", pane, "preset", s.Preset)
	if err := w.Apply(); err != nil {
		return false, err
	}
	if !selectPane {
		return true, nil
	}
	_, err := w.T.Run("select-pane", "-t", pane)
	return true, err
}

// Slots is how many agent panes the current preset holds.
func (w *Wall) Slots() (int, error) {
	s, err := w.Read()
	if err != nil {
		return 0, err
	}
	return s.Slots(), nil
}

func remove(ps []tmux.PaneInfo, id string) []tmux.PaneInfo {
	var out []tmux.PaneInfo
	for _, p := range ps {
		if p.ID != id {
			out = append(out, p)
		}
	}
	return out
}

// swapInto puts an incoming pane into the slot of a pane already on the wall,
// in place: no break-pane/join-pane and no re-layout, so the geometry the user
// was looking at is exactly preserved. The replaced pane lands in the window
// the incoming pane came from, which is renamed after it as in breakOut.
// Focus is not moved: `swap-pane -d` keeps the active pane where it was (if
// the replaced slot had focus, the incoming pane in that slot now has it).
func (w *Wall) swapInto(incoming, slot, now string) (bool, error) {
	if _, err := w.T.Run("swap-pane", "-d", "-s", incoming, "-t", slot); err != nil {
		return false, err
	}
	w.T.SetPaneOption(incoming, "@role", "wall")
	w.T.SetPaneOption(incoming, "@fleet_used", now)
	w.T.SetPaneOption(slot, "@role", "agent")
	w.T.Run("rename-window", "-t", slot, w.agentWindow(slot))
	// The target stays on this slot, now holding the incoming pane, so card
	// after card cycles agents through one slot (DESIGN.md §5e).
	w.setOpt("@fleet_target", incoming)
	logx.For("wall").Info("into the target slot", "pane", incoming, "replaced", slot)
	// The shape is unchanged, but the pane id in that leaf is not: re-apply so
	// @fleet_layout matches reality (with no emphasis this moves nothing).
	if err := w.Apply(); err != nil {
		return false, err
	}
	return true, nil
}

// agentWindow is the window name a pane belongs in when it is off the wall.
func (w *Wall) agentWindow(pane string) string {
	name, _ := w.T.Run("display-message", "-p", "-t", pane, "#{@fleet_agent}")
	if name = strings.TrimSpace(name); name == "" {
		return "loose"
	}
	return "ag-" + name
}

func (w *Wall) breakOut(pane string) {
	name, _ := w.T.Run("display-message", "-p", "-t", pane, "#{@fleet_agent}")
	win := w.agentWindow(pane)
	session := strings.SplitN(w.Target, ":", 2)[0]
	if _, err := w.T.Run("break-pane", "-d", "-s", pane, "-t", session+":", "-n", win); err == nil {
		w.T.SetPaneOption(pane, "@role", "agent")
		logx.For("wall").Info("off wall", "pane", pane, "agent", name)
	}
	if w.opt("@fleet_target") == pane {
		w.setOpt("@fleet_target", "") // a target off the wall is no target
	}
}

// HideFocused hides the active pane of the main window if it is a wall pane.
// Otherwise it shows a tmux message and does nothing.
func (w *Wall) HideFocused() error {
	out, err := w.T.Run("display-message", "-p", "-t", w.Target, "#{pane_id}\t#{@role}")
	if err != nil {
		return err
	}
	id, role, _ := strings.Cut(out, "\t")
	if role != "wall" {
		w.T.Run("display-message", "M-x: focus a wall pane first (admiral/overview stay)")
		return nil
	}
	return w.Hide(id)
}

// Hide returns a pane to its hidden window.
func (w *Wall) Hide(pane string) error { w.breakOut(pane); return w.Apply() }

// SetPreset changes the wall layout, evicting panes that no longer fit.
func (w *Wall) SetPreset(p string) error {
	p = strings.ToLower(p)
	if _, ok := layout.Presets[p]; !ok {
		return fmt.Errorf("unknown preset %q (1 2 4 6 9 m)", p)
	}
	w.setOpt("@fleet_wall", p)
	s, err := w.Read()
	if err != nil {
		return err
	}
	// Shrinking evicts only evictable panes; protected panes stay even if that
	// leaves more panes than the cap (the grid resizes to fit them).
	slots := s.Slots()
	if s.Capped() {
		w.T.Run("display-message", "-t", w.Target, steppedMsg(p, slots))
	}
	b := w.newBatch(s)
	target := w.opt("@fleet_target")
	for len(s.Wall) > slots {
		id, ok := EvictCandidate(s.Wall)
		if !ok {
			break
		}
		b.breakOut(id)
		if id == target {
			w.setOpt("@fleet_target", "") // a target off the wall is no target
		}
		s.Wall = remove(s.Wall, id)
	}
	added := w.fill(s, slots, b)
	logx.For("wall").Info("preset", "preset", p, "slots", slots, "on_wall", len(s.Wall), "added", added)
	_, err = b.commit(s)
	return err
}

// fill joins hidden agent panes onto the wall until it has slots panes,
// most recently used first. Returns how many were added.
func (w *Wall) fill(s *State, slots int, b *batch) int {
	if len(s.Wall) >= slots {
		return 0
	}
	all, err := w.T.ListPanes("")
	if err != nil {
		return 0
	}
	session := strings.SplitN(w.Target, ":", 2)[0]
	var hidden []tmux.PaneInfo
	for _, p := range all {
		if p.Session == session && p.Agent != "" && !p.Dead && strings.HasPrefix(p.Window, "ag-") {
			hidden = append(hidden, p)
		}
	}
	sort.SliceStable(hidden, func(i, j int) bool { return hidden[i].Used > hidden[j].Used })
	added := 0
	for _, p := range hidden {
		if len(s.Wall) >= slots {
			break
		}
		target := s.Ov
		if len(s.Wall) > 0 {
			target = s.Wall[len(s.Wall)-1].ID
		}
		b.join(p.ID, target)
		p.Role = "wall"
		s.Wall = append(s.Wall, p)
		added++
	}
	return added
}

// ToggleSup hides or shows the admiral pane.
func (w *Wall) ToggleSup() error {
	s, err := w.Read()
	if err != nil {
		return err
	}
	session := strings.SplitN(w.Target, ":", 2)[0]
	if len(s.Sups) > 0 {
		for _, id := range s.Sups {
			if _, err := w.T.Run("break-pane", "-d", "-s", id, "-t", session+":", "-n", "sup-hidden"); err != nil {
				return err
			}
		}
		return w.Apply()
	}
	panes, _ := w.T.ListPanes("")
	sups := panes[:0:0]
	for _, p := range panes {
		if p.Role == "sup" && p.Session == session {
			sups = append(sups, p)
		}
	}
	sort.SliceStable(sups, func(i, j int) bool { return sups[i].Admiral == "" && sups[j].Admiral != "" })
	for i, p := range sups {
		{
			target := s.Ov
			if i > 0 {
				target = sups[i-1].ID
			}
			flag := "-b"
			if s.Cfg.SupSide == "right" {
				flag = ""
			}
			args := []string{"join-pane", "-d", "-h", "-l", strconv.Itoa(s.Cfg.SupCols(s.W)), "-s", p.ID, "-t", target}
			if i > 0 {
				args = []string{"join-pane", "-d", "-v", "-s", p.ID, "-t", target}
			} else if flag != "" {
				args = append(args, flag)
			}
			if _, err := w.T.Run(args...); err != nil {
				return err
			}
		}
	}
	if len(sups) > 0 {
		return w.Apply()
	}
	return fmt.Errorf("no admiral pane")
}

// SwapSide moves the admiral to the other side.
func (w *Wall) SwapSide() error {
	s, err := w.Read()
	if err != nil {
		return err
	}
	side := "right"
	if s.Cfg.SupSide == "right" {
		side = "left"
	}
	w.setOpt("@fleet_sup_side", side)
	return w.Apply()
}

// ToggleChips switches the overview between tiles and chips-only.
func (w *Wall) ToggleChips() error {
	v := "1"
	if w.opt("@fleet_chips") == "1" {
		v = "0"
	}
	w.setOpt("@fleet_chips", v)
	return w.Apply()
}

// OnWall returns agent names currently on the wall.
func (w *Wall) OnWall() map[string]bool {
	out := map[string]bool{}
	panes, err := w.listPanes(w.Target)
	if err != nil {
		return out
	}
	for _, p := range panes {
		if p.Agent != "" {
			out[p.Agent] = true
		}
	}
	return out
}
