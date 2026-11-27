package main

import (
	"fmt"
	"os"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/theme"
)

// The overview's theme picker (T, ADR-175): ui.ThemeBackend.

// ThemeChrome builds the set-option half of configure from the active
// palette now, on the UI goroutine, and returns the work of applying it. Key
// bindings and hooks carry no colour, so a preview skips them; Save runs the
// whole of configure.
func (b *backend) ThemeChrome() func() {
	var cmds [][]string
	for _, a := range append(tmuxCommands(b.f), extTmux(b.f)...) {
		if len(a) > 0 && a[0] == "set-option" {
			cmds = append(cmds, a)
		}
	}
	n := b.themeSeq.Add(1)
	return func() {
		b.themeMu.Lock()
		defer b.themeMu.Unlock()
		if n < b.themeRan { // a later preview already ran
			return
		}
		b.themeRan = n
		for _, a := range cmds {
			b.f.T.Run(a...)
		}
	}
}

// SaveTheme writes the theme to config.toml and does what `fleet up` does
// after a theme change, without touching an agent (ADR-55): the full chrome,
// fresh status tags and a watcher that loaded the new palette. The commands
// are built here, on the UI goroutine that owns the palette. This overview
// already shows it, so its pane is stamped instead of respawned.
func (b *backend) SaveTheme(name string) func() error {
	cmds := append(tmuxCommands(b.f), extTmux(b.f)...)
	return func() error {
		if err := prefs.SetTheme(name); err != nil {
			return fmt.Errorf("save theme: %w", err)
		}
		b.themeMu.Lock()
		defer b.themeMu.Unlock()
		configureWith(b.f, cmds)
		rethemeTo(b.f, name, true)
		startWatcher(b.f)
		if p := os.Getenv("TMUX_PANE"); p != "" {
			b.f.T.SetPaneOption(p, "@fleet_theme", name)
		}
		return nil
	}
}

// themeReloader watches config.toml and the theme files for a live reload
// (ADR-201).
func themeReloader() *theme.Reloader {
	return &theme.Reloader{Config: prefs.Path(), Dir: prefs.ThemeDir(), Name: func() (string, string, error) {
		p, err := prefs.Load()
		if err != nil {
			return "", "", fmt.Errorf("read config: %w", err)
		}
		return p.Theme(), p.Stray(), nil
	}}
}

// reloadTheme is the watcher's half of a live theme reload: a changed
// palette re-colours the tmux chrome and drops the status tags, so this
// refresh re-draws every agent's pane border from it. The overview and the
// web board reload their own copy.
func reloadTheme(f *fleet.Fleet, r *theme.Reloader) {
	changed, warn := r.Check()
	if warn != "" {
		logx.For("theme").Warn(warn)
	}
	if !changed || !f.T.HasSession(fleet.Session) {
		return
	}
	logx.For("theme").Info("theme reloaded", "theme", theme.Active().Name)
	configure(f)
	dropStatusTags(f)
	name := theme.Active().Name
	f.T.Run("set-option", "-t", fleet.Session, "@fleet_theme", name)
	// The overview reloads itself; stamp its pane so the next fleet up does
	// not respawn it for a theme it already shows.
	panes, _ := f.T.ListPanes("")
	for _, p := range panes {
		if p.Role == "ov" {
			f.T.SetPaneOption(p.ID, "@fleet_theme", name)
		}
	}
}
