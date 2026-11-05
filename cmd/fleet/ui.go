package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/team"
	"github.com/sturlabragason/fleet/internal/tools"
	"github.com/sturlabragason/fleet/internal/ui"
	"github.com/sturlabragason/fleet/internal/wall"
)

// backend adapts fleet + wall to ui.Backend.
type backend struct {
	f       *fleet.Fleet
	w       *wall.Wall
	enabled bool
	supProf string // last @fleet_admiral_profile set on the admiral border
}

func (b *backend) Refresh() ([]*board.Agent, error) { return b.f.Refresh() }

// ApplyWall re-tiles after every refresh, so emphasis follows status changes
// and the dwell/hold clock actually ticks. It is the cached apply: an
// unchanged layout costs a handful of read-only tmux calls and never touches
// a pane. It is a no-op (and never logs) when the TUI is not running inside
// the fleet server.
func (b *backend) ApplyWall() error {
	if !b.enabled {
		return nil
	}
	return b.w.ApplyCached()
}
func (b *backend) WallEnabled() bool { return b.enabled }

// Reports is how many reports the admiral has not read: the header badge (ADR-36).
func (b *backend) Reports() int { return b.f.UnreadReports() }
func (b *backend) OnWall() map[string]bool {
	if !b.enabled {
		return map[string]bool{}
	}
	return b.w.OnWall()
}
func (b *backend) Teams() map[string]team.Info { return b.f.Teams.ByAgent() }
func (b *backend) Show(name string) (bool, error) {
	p, err := b.f.Pane(name)
	if err != nil {
		return false, err
	}
	return b.w.Show(p)
}
func (b *backend) Add(name string) (bool, error) {
	p, err := b.f.Pane(name)
	if err != nil {
		return false, err
	}
	return b.w.Add(p)
}
func (b *backend) Next(dir int) (string, bool, error) {
	if !b.enabled {
		return "", true, nil
	}
	return b.w.Next(dir)
}
func (b *backend) Target() string {
	if !b.enabled {
		return ""
	}
	return b.w.TargetAgent()
}
func (b *backend) Untarget() error {
	if !b.enabled {
		return nil
	}
	return b.w.Untarget()
}
func (b *backend) Undo() (string, string, error) {
	if !b.enabled {
		return wall.NothingToUndoMsg, "", nil
	}
	return b.w.Undo()
}
func (b *backend) TogglePin(name string) (bool, error) {
	p, err := b.f.Pane(name)
	if err != nil {
		return false, err
	}
	return b.w.TogglePin(p)
}
func (b *backend) Pinned() map[string]bool {
	if !b.enabled {
		return map[string]bool{}
	}
	return b.w.Pinned()
}

// Preview opens the live preview popup (the overview's Space).
func (b *backend) Preview(name string) error {
	_, err := b.f.T.Run(previewPopupArgs(tools.Quote(b.f.Exe), name, b.w.TargetAgent() != "")...)
	return err
}

// Menu opens the right-click menu for name. x,y are overview cells; tmux
// wants client cells, so the overview pane's offset is added.
func (b *backend) Menu(name string, x, y int) error {
	pane := os.Getenv("TMUX_PANE")
	off, err := b.f.T.Run("display-message", "-p", "-t", pane, "#{pane_left} #{pane_top}")
	if err != nil {
		return err
	}
	var left, top int
	fmt.Sscan(off, &left, &top)
	target, pins := b.w.TargetAgent(), b.w.Pinned()
	st := menuState{onWall: b.w.OnWall()[name], hasTarget: target != "", pinned: pins[name], targetPinned: pins[target]}
	args := menuArgs(tools.Quote(b.f.Exe), name, x+left, y+top, st)
	_, err = b.f.T.Run(append(args[:1:1], append([]string{"-t", pane}, args[1:]...)...)...)
	return err
}
func (b *backend) Hide(name string) error {
	p, err := b.f.Pane(name)
	if err != nil {
		return err
	}
	return b.w.Hide(p)
}
func (b *backend) Preset(p string) error {
	if !b.enabled {
		return nil
	}
	return b.w.SetPreset(p)
}

func (b *backend) SupCtx() (string, string) {
	if !b.enabled {
		return "", ""
	}
	b.syncAdmiralProfile()
	used, max, ok := supervisor.CurrentFill(b.f.Home, b.f.T, mainTarget())
	if !ok {
		return "", ""
	}
	pct := supervisor.Pct(used, max)
	return fmt.Sprintf("%d%%", pct), supervisor.Band(pct)
}

func (b *backend) SetSupCtx(label, color string) error {
	if !b.enabled {
		return nil
	}
	pane, err := supervisor.FindPane(b.f.T, mainTarget())
	if err != nil {
		return nil // no admiral, nothing to colour
	}
	if label == "" {
		b.f.T.Run("set-option", "-p", "-u", "-t", pane, "@fleet_ctx")
		_, err = b.f.T.Run("set-option", "-p", "-u", "-t", pane, "@fleet_ctx_color")
		return err
	}
	b.f.T.SetPaneOption(pane, "@fleet_ctx_color", color)
	return b.f.T.SetPaneOption(pane, "@fleet_ctx", label)
}

func (b *backend) ResetSup() error {
	_, err := b.f.T.Run(confirmResetArgs(b.f.Exe)...)
	return err
}

// PickProfile opens the admiral profile picker popup (the overview's P).
func (b *backend) PickProfile() error {
	_, err := b.f.T.Run(pickPopupArgs(b.f)...)
	return err
}

// syncAdmiralProfile keeps the admiral border's profile in step with
// session.json; tmux is only touched when the profile changed.
func (b *backend) syncAdmiralProfile() {
	s, ok := supervisor.Load(b.f.Home)
	if !ok || s.Profile == "" || s.Profile == b.supProf {
		return
	}
	pane, err := supervisor.FindPane(b.f.T, mainTarget())
	if err != nil {
		return
	}
	if b.f.T.SetPaneOption(pane, "@fleet_admiral_profile", s.Profile) == nil {
		b.supProf = s.Profile
	}
}

// Changes opens the what's new popup (DESIGN.md §5d) on the fleet client (the overview's g).
func (b *backend) Changes() error {
	_, err := b.f.T.Run(changesPopupArgs(tools.Quote(b.f.Exe))...)
	return err
}

// insideFleet reports whether this process runs in a pane of the fleet server.
func insideFleet(f *fleet.Fleet) bool {
	tm := os.Getenv("TMUX")
	if tm == "" || os.Getenv("TMUX_PANE") == "" {
		return false
	}
	sock := strings.SplitN(tm, ",", 2)[0]
	return filepath.Base(sock) == fleet.Socket
}

func cmdUI(f *fleet.Fleet) error {
	zone.NewGlobal()
	// Off-wall panes are captured every 5 s instead of every poll (DESIGN §7a).
	f.OffWallEvery = 5 * time.Second
	// Only the overview TUI starts queued spawns when a slot frees (§7a).
	f.DrainQueue = true
	w := wall.New(f.T, fleet.Session)
	w.KeepFocus = true // keys typed after a click/Enter/Tab/a must still reach the overview
	be := &backend{f: f, w: w, enabled: insideFleet(f)}
	logx.For("ui").Info("start", "wall", be.enabled)
	p := tea.NewProgram(ui.New(be, f.Home), tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	if err != nil {
		logx.For("ui").Error("exit", "err", err)
	}
	return err
}
