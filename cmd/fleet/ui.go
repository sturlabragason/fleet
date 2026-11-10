package main

import (
	"context"
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
	"github.com/sturlabragason/fleet/internal/theme"
	"github.com/sturlabragason/fleet/internal/tools"
	"github.com/sturlabragason/fleet/internal/ui"
	"github.com/sturlabragason/fleet/internal/wall"
	"github.com/sturlabragason/fleet/internal/watch"
)

// backend adapts fleet + wall to ui.Backend.
type backend struct {
	f        *fleet.Fleet
	w        *wall.Wall
	enabled  bool
	owners   map[string]string // pane -> owner chip last set on its border
	supProf  string            // last @fleet_admiral_profile set on the admiral border
	otherCtx map[string]string // other admirals' pane -> profile|ctx|color last set
}

// Refresh reads the board when the watcher is live, and captures the panes
// itself when it is not. One refresher at a time is what keeps the border
// titles, the board and these cards from disagreeing (ADR-49), and it halves
// the tmux calls while the overview is open. The admiral wake is not a
// refresh and runs either way: skipping it here is what let reports sit
// unseen while a watcher ran. The status-line badge is the watcher's alone
// then: an overview left on an older binary set it differently every poll
// and it blinked (ADR-75).
func (b *backend) Refresh() ([]*board.Agent, error) {
	if watch.Live(b.f.Home, time.Now()) {
		b.f.RetryWake()
		return b.f.Store.Load().Sorted(), nil
	}
	return b.f.Refresh()
}

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
	st := menuState{multi: len(b.f.AdmiralNames()) > 1, onWall: b.w.OnWall()[name], hasTarget: target != "", pinned: pins[name], targetPinned: pins[target]}
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
	b.syncOtherCtx()
	used, max, ok := supervisor.CurrentFill(b.f.Home, b.f.T, mainTarget())
	if !ok {
		return "", ""
	}
	return fmt.Sprintf("%d%%", supervisor.Pct(used, max)), supervisor.BandFill(used, max)
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

func (b *backend) ResetSup() error { return cmdAdmiralAct(b.f, []string{"reset"}) }

// PickProfile opens the admiral profile picker popup (the overview's P).
func (b *backend) PickProfile() error { return cmdAdmiralAct(b.f, []string{"pick"}) }

// Admirals lists the admirals with their colours (the overview's owner chips).
func (b *backend) Admirals() []ui.AdmiralInfo {
	var out []ui.AdmiralInfo
	for _, n := range b.f.AdmiralNames() {
		prof := b.f.Prefs.AdmiralProfile()
		if a, ok := supervisor.Find(b.f.Home, n); ok && a.Profile != "" {
			prof = a.Profile
		} else if s, ok := supervisor.LoadOf(b.f.Home, n); ok && s.Profile != "" {
			prof = s.Profile
		}
		out = append(out, ui.AdmiralInfo{Name: n, Profile: prof, Color: theme.AdmiralColor(supervisor.Slot(b.f.Home, n))})
	}
	b.syncOwners(len(out) > 1)
	return out
}

func (b *backend) BadgeBy() string { return b.f.BadgeBy() }

// PickOwner opens the hand-over menu for agent (the overview's O).
func (b *backend) PickOwner(agent string) error { return ownerMenu(b.f, agent) }

// syncOwners tags each agent pane with its owner's chip for the wall border
// while several admirals exist, and clears the tags when only one is left.
// Only changes reach tmux.
func (b *backend) syncOwners(multi bool) {
	if !b.enabled {
		return
	}
	panes, err := b.f.T.ListPanes(fleet.Session + ":")
	if err != nil {
		return
	}
	ag := b.f.Store.Load().Agents
	names := b.f.AdmiralNames()
	ini := ui.OwnerInitials(names)
	if b.owners == nil {
		b.owners = map[string]string{}
	}
	for _, p := range panes {
		if p.Agent == "" || p.Role == "sup" || p.Role == "ov" {
			continue
		}
		want := ""
		if a, ok := ag[p.Agent]; ok && multi {
			want = ini[a.Owner()] + "|" + theme.AdmiralColor(supervisor.Slot(b.f.Home, a.Owner()))
		}
		if b.owners[p.ID] == want {
			continue
		}
		b.owners[p.ID] = want
		if want == "" {
			b.f.T.UnsetPaneOption(p.ID, "@fleet_owner")
			b.f.T.UnsetPaneOption(p.ID, "@fleet_owner_color")
			continue
		}
		txt, col, _ := strings.Cut(want, "|")
		b.f.T.SetPaneOption(p.ID, "@fleet_owner", txt)
		b.f.T.SetPaneOption(p.ID, "@fleet_owner_color", col)
	}
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

// syncOtherCtx keeps the context meter and profile on every other admiral's
// border current; main's go through SupCtx and SetSupCtx.
func (b *backend) syncOtherCtx() {
	for _, a := range supervisor.Admirals(b.f.Home) {
		pane, err := supervisor.FindPaneOf(b.f.T, mainTarget(), a.Name)
		if err != nil {
			continue
		}
		var prof string
		if s, ok := supervisor.LoadOf(b.f.Home, a.Name); ok {
			prof = s.Profile
		}
		var ctx, color string
		if used, max, ok := supervisor.CurrentFillOf(b.f.Home, b.f.T, mainTarget(), a.Name); ok {
			ctx, color = fmt.Sprintf("%d%%", supervisor.Pct(used, max)), supervisor.BandFill(used, max)
		}
		want := prof + "|" + ctx + "|" + color
		if b.otherCtx[pane] == want {
			continue // like syncOwners: only changes reach tmux
		}
		if b.otherCtx == nil {
			b.otherCtx = map[string]string{}
		}
		b.otherCtx[pane] = want
		if prof != "" {
			b.f.T.SetPaneOption(pane, "@fleet_admiral_profile", prof)
		}
		if ctx == "" {
			b.f.T.UnsetPaneOption(pane, "@fleet_ctx")
			continue
		}
		b.f.T.SetPaneOption(pane, "@fleet_ctx_color", color)
		b.f.T.SetPaneOption(pane, "@fleet_ctx", ctx)
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
	// The overview TUI starts queued spawns, runs the memory guard and does
	// the once-a-poll housekeeping (§7, §7a).
	f.DrainQueue = true
	f.Housekeeping = true
	// It also runs done-checks nobody else picked up, off the UI loop (ADR-43).
	f.Async()
	w := wall.New(f.T, fleet.Session)
	w.KeepFocus = true // keys typed after a click/Enter/Tab/a must still reach the overview
	be := &backend{f: f, w: w, enabled: insideFleet(f)}
	logx.For("ui").Info("start", "wall", be.enabled)
	p := tea.NewProgram(ui.New(be, f.Home), tea.WithAltScreen(), tea.WithMouseCellMotion())
	// A rebuilt bin/fleet takes over this pane (ADR-62).
	re := newUIReexec(f.Exe, "", probeBinary, p.Quit)
	ctx, stop := context.WithCancel(context.Background())
	go re.run(ctx, watch.StopEvery)
	_, err := p.Run()
	stop()
	handOver := re.halt()
	if err != nil {
		logx.For("ui").Error("exit", "err", err)
		return err
	}
	if handOver {
		if err := re.exec(f.Exe); err != nil {
			logx.For("ui").Error("re-exec", "err", err)
			return fmt.Errorf("restart the overview on the rebuilt binary: %w (run fleet up)", err)
		}
	}
	return nil
}
