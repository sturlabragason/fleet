package main

import (
	"fmt"
	"os"

	"github.com/sturlabragason/fleet/internal/prefs"
)

// The overview's theme picker (t, ADR-175): ui.ThemeBackend.

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
// fresh status tags and a watcher that loaded the new palette. This overview
// already shows it, so its pane is stamped instead of respawned.
func (b *backend) SaveTheme(name string) error {
	if err := prefs.SetTheme(name); err != nil {
		return fmt.Errorf("save theme: %w", err)
	}
	b.themeMu.Lock()
	defer b.themeMu.Unlock()
	configure(b.f)
	retheme(b.f)
	startWatcher(b.f)
	if p := os.Getenv("TMUX_PANE"); p != "" {
		b.f.T.SetPaneOption(p, "@fleet_theme", name)
	}
	return nil
}
