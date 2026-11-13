package wall

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// Admiral placement (DESIGN §6.12, ADR-123). Every admiral lives in the
// main window's left column, main on top and the others stacked under it.
// An admiral the user did not hide but that ended up in another window
// (two-monitor mode undone, a main admiral pane recreated, a window that was
// too short for the column) is pulled back by a forced Apply. Panes are only
// ever moved with join-pane / break-pane, never respawned, so the admiral's
// process and any remote session attached to it keep running.

// HiddenAdmiralOption marks an admiral pane the user took out of the column
// on purpose (fleet wall admiral-hide); gather leaves it where it is.
const HiddenAdmiralOption = "@fleet_admiral_hidden"

// AdmiralMsg answers an admiral show or hide.
func AdmiralMsg(name string, shown bool) string {
	if shown {
		return "admiral " + name + " shown"
	}
	return "admiral " + name + " hidden"
}

// NoRoomMsg is why a shown admiral stays out of the column.
const NoRoomMsg = "no room in the admiral column for another admiral (the window is too short); it shows when there is"

func isMainAdmiral(name string) bool { return name == "" || name == "main" }

// window is the window name of w.Target ("main").
func (w *Wall) window() string {
	_, win, _ := strings.Cut(w.Target, ":")
	return win
}

func (w *Wall) session() string {
	sess, _, _ := strings.Cut(w.Target, ":")
	return sess
}

// strayAdmirals lists the session's non-main admiral panes outside the main
// window that the user did not hide, oldest pane first.
func (w *Wall) strayAdmirals() []tmux.PaneInfo {
	all, err := w.T.ListPanes("")
	if err != nil {
		return nil
	}
	var out []tmux.PaneInfo
	for _, p := range all {
		if p.Session != w.session() || p.Role != "sup" || isMainAdmiral(p.Admiral) || p.Dead || p.Window == w.window() {
			continue
		}
		if w.T.PaneOption(p.ID, HiddenAdmiralOption) == "1" {
			continue
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return paneNum(out[i].ID) < paneNum(out[j].ID) })
	return out
}

func paneNum(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "%"))
	return n
}

// gather joins stray admirals under the last admiral of the column while
// main is in the main window and the column has room for them; it returns
// how many it moved. Without main in the window (Alt-s hid every admiral,
// or two-monitor mode moved them to their own window) it does nothing.
func (w *Wall) gather(s *State) int {
	if len(s.Sups) == 0 || s.SupNames[0] != "main" {
		return 0
	}
	strays := w.strayAdmirals()
	if len(strays) == 0 {
		return 0
	}
	if !w.lock() {
		return 0
	}
	defer w.unlock()
	last, n, moved := s.Sups[len(s.Sups)-1], len(s.Sups), 0
	for _, p := range strays {
		if s.Cfg.SupFit(s.H, n+1) < n+1 {
			break
		}
		if _, err := w.T.Run("join-pane", "-d", "-v", "-s", p.ID, "-t", last); err != nil {
			logx.For("wall").Error("gather admiral", "admiral", p.Admiral, "pane", p.ID, "err", err)
			continue
		}
		logx.For("wall").Info("gathered admiral", "admiral", p.Admiral, "pane", p.ID, "from", p.Window)
		last, n, moved = p.ID, n+1, moved+1
	}
	return moved
}

// admiralPane finds a non-main admiral's pane anywhere in the session.
func (w *Wall) admiralPane(name string) (tmux.PaneInfo, error) {
	if isMainAdmiral(name) {
		return tmux.PaneInfo{}, fmt.Errorf("main is shown and hidden with the other admirals: fleet wall sup (Alt-s)")
	}
	all, err := w.T.ListPanes("")
	if err != nil {
		return tmux.PaneInfo{}, err
	}
	for _, p := range all {
		if p.Session == w.session() && p.Role == "sup" && p.Admiral == name {
			return p, nil
		}
	}
	return tmux.PaneInfo{}, fmt.Errorf("no admiral pane %q", name)
}

// HideAdmiral takes a non-main admiral out of the column into a window of
// its own and keeps it out until ShowAdmiral. Its process keeps running.
func (w *Wall) HideAdmiral(name string) error {
	p, err := w.admiralPane(name)
	if err != nil {
		return err
	}
	w.T.SetPaneOption(p.ID, HiddenAdmiralOption, "1")
	if p.Window == w.window() {
		if _, err := w.T.Run("break-pane", "-d", "-s", p.ID, "-t", w.session()+":", "-n", "sup-hidden"); err != nil {
			return err
		}
	}
	return w.Apply()
}

// ShowAdmiral puts a hidden admiral back under the others. With main away
// from the main window (Alt-s, two monitors) it joins main wherever main is.
// shown is false when the column has no room for it yet.
func (w *Wall) ShowAdmiral(name string) (shown bool, err error) {
	p, err := w.admiralPane(name)
	if err != nil {
		return false, err
	}
	w.T.UnsetPaneOption(p.ID, HiddenAdmiralOption)
	if p.Window != w.window() {
		if main := w.mainAdmiralElsewhere(); main.ID != "" && main.WindowID != p.WindowID {
			if _, err := w.T.Run("join-pane", "-d", "-v", "-s", p.ID, "-t", main.ID); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	if err := w.Apply(); err != nil {
		return false, err
	}
	p, err = w.admiralPane(name)
	if err != nil {
		return false, err
	}
	return p.Window == w.window(), nil
}

// mainAdmiralElsewhere is main's pane when it is in another window than
// the main one; the zero value when it is in the main window or missing.
func (w *Wall) mainAdmiralElsewhere() tmux.PaneInfo {
	all, _ := w.T.ListPanes("")
	for _, p := range all {
		if p.Session == w.session() && p.Role == "sup" && isMainAdmiral(p.Admiral) && p.Window != w.window() {
			return p
		}
	}
	return tmux.PaneInfo{}
}
