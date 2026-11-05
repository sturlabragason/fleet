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
}

// New returns a wall for session:main.
func New(t *tmux.Client, session string) *Wall {
	return &Wall{T: t, Target: session + ":main", Cfg: layout.Default}
}

// State is the parsed main window.
type State struct {
	W, H      int
	Sup, Ov   string
	Wall      []tmux.PaneInfo // in window order
	Preset    string
	Cfg       layout.Config
	SupHidden string // pane id of hidden admiral, if any
}

func (w *Wall) opt(name string) string {
	s, _ := w.T.Run("show-options", "-wqv", "-t", w.Target, name)
	return strings.TrimSpace(s)
}

func (w *Wall) setOpt(name, val string) {
	w.T.Run("set-option", "-w", "-t", w.Target, name, val)
}

// Read loads the current state.
func (w *Wall) Read() (*State, error) {
	dims, err := w.T.Run("display-message", "-p", "-t", w.Target, "#{window_width} #{window_height}")
	if err != nil {
		return nil, err
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
	panes, err := w.T.ListPanes(w.Target)
	if err != nil {
		return nil, err
	}
	for _, p := range panes {
		switch p.Role {
		case "sup":
			s.Sup = p.ID
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
	s, err := w.Read()
	if err != nil {
		return err
	}
	var ids []string
	for _, p := range s.Wall {
		ids = append(ids, p.ID)
	}
	w.syncNextOut(s)
	lay, leaves := s.Cfg.Render(layout.Input{W: s.W, H: s.H, Sup: s.Sup, Ov: s.Ov, Wall: ids, Preset: s.Preset, Focus: w.emphasis(s)})
	// An unchanged layout is never re-applied: agent TUIs reflow on every resize.
	if !force && lay == w.opt("@fleet_layout") {
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
	w.setOpt("@fleet_layout", lay)
	if s.Sup != "" {
		w.T.Run("resize-pane", "-t", s.Sup, "-x", strconv.Itoa(s.Cfg.SupCols(s.W)))
	}
	return nil
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
	for i, id := range want {
		if i >= len(cur) || cur[i] == id {
			continue
		}
		j := index(cur, id)
		if j < 0 {
			continue
		}
		w.T.Run("swap-pane", "-d", "-s", cur[i], "-t", id)
		swapped[cur[i]], swapped[id] = true, true
		cur[i], cur[j] = cur[j], cur[i]
	}
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
	slots := s.Cfg.Slots(s.Preset)
	for len(s.Wall) >= slots {
		id, ok := EvictCandidate(s.Wall)
		if !ok {
			// Everything on the wall needs me: leave it alone and say so.
			w.T.Run("display-message", "-t", w.Target, FullMsg)
			log.Info("wall full, nothing evictable", "pane", pane, "slots", slots)
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
		if _, err2 := w.T.Run("join-pane", "-d", "-s", pane, "-t", s.Sup); err2 != nil {
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
	return s.Cfg.Slots(s.Preset), nil
}

// FullMsg is shown when every wall pane is protected from eviction.
const FullMsg = "wall full: every pane is waiting or failing (M-x to free one)"

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
	slots := s.Cfg.Slots(p)
	for len(s.Wall) > slots {
		id, ok := EvictCandidate(s.Wall)
		if !ok {
			break
		}
		w.breakOut(id)
		s.Wall = remove(s.Wall, id)
	}
	added := w.fill(s, slots)
	logx.For("wall").Info("preset", "preset", p, "slots", slots, "on_wall", len(s.Wall), "added", added)
	return w.Apply()
}

// fill joins hidden agent panes onto the wall until it has slots panes,
// most recently used first. Returns how many were added.
func (w *Wall) fill(s *State, slots int) int {
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
		if _, err := w.T.Run("join-pane", "-d", "-s", p.ID, "-t", target); err != nil {
			continue
		}
		w.T.SetPaneOption(p.ID, "@role", "wall")
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
	if s.Sup != "" {
		if _, err := w.T.Run("break-pane", "-d", "-s", s.Sup, "-t", session+":", "-n", "sup-hidden"); err != nil {
			return err
		}
		return w.Apply()
	}
	panes, _ := w.T.ListPanes("")
	for _, p := range panes {
		if p.Role == "sup" {
			flag := "-b"
			if s.Cfg.SupSide == "right" {
				flag = ""
			}
			args := []string{"join-pane", "-d", "-h", "-l", strconv.Itoa(s.Cfg.SupCols(s.W)), "-s", p.ID, "-t", s.Ov}
			if flag != "" {
				args = append(args, flag)
			}
			if _, err := w.T.Run(args...); err != nil {
				return err
			}
			return w.Apply()
		}
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
	panes, err := w.T.ListPanes(w.Target)
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
