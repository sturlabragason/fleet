package ui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/theme"
)

// ThemeBackend is what a Backend adds for the theme picker (t, ADR-175).
// Optional: without it t says so.
type ThemeBackend interface {
	// ThemeChrome returns the work that re-colours the tmux chrome with the
	// active palette. It is built on the UI goroutine, which owns the
	// palette, and run off it; a later call's work supersedes an earlier one.
	ThemeChrome() func()
	// SaveTheme returns the work that writes [defaults] theme = name to
	// config.toml and applies the active palette (name) to the rest of the
	// running fleet; built on the UI goroutine like ThemeChrome.
	SaveTheme(name string) func() error
}

// themePick is the open picker: every palette, the cursor, and the theme to
// restore on Esc.
type themePick struct {
	names []string
	cur   int
	orig  string
}

// pickRows is how many theme rows the picker shows at once; the tiles stay
// visible below them, so the preview can be seen.
const pickRows = 8

type themeSavedMsg struct {
	name string
	err  error
}

// openThemes is t: open the picker on the active theme.
func (m *Model) openThemes() tea.Cmd {
	if _, ok := m.be.(ThemeBackend); !ok || !m.be.WallEnabled() {
		m.msg = "t needs fleet up"
		return nil
	}
	orig := theme.Active().Name
	names := theme.Names()
	m.tp = &themePick{names: names, orig: orig, cur: max(slices.Index(names, orig), 0)}
	return nil
}

// themeKey moves, saves or cancels. Every move applies the palette at once:
// this process re-styles from theme's OnChange hooks, the tmux chrome
// through the backend.
func (m *Model) themeKey(k string) tea.Cmd {
	p := m.tp
	switch k {
	case "up", "k", "down", "j", "pgup", "pgdown", "home", "end":
		c := p.cur + map[string]int{"up": -1, "k": -1, "down": 1, "j": 1, "pgup": -pickRows, "pgdown": pickRows, "home": -len(p.names), "end": len(p.names)}[k]
		c = min(max(c, 0), len(p.names)-1)
		if c == p.cur {
			return nil
		}
		p.cur = c
		return m.previewTheme(p.names[c])
	case "enter":
		name := p.names[p.cur]
		save := m.be.(ThemeBackend).SaveTheme(name)
		m.tp = nil
		return func() tea.Msg { return themeSavedMsg{name, save()} }
	case "esc", "q", "t":
		m.tp = nil
		if theme.Active().Name == p.orig {
			return nil
		}
		m.msg, m.msgAt = "theme unchanged: "+p.orig, m.now()
		return m.previewTheme(p.orig)
	case "ctrl+c": // quit, but leave the tmux chrome on the saved theme
		m.tp = nil
		if theme.Active().Name == p.orig {
			return tea.Quit
		}
		return tea.Sequence(m.previewTheme(p.orig), tea.Quit)
	}
	return nil
}

// previewTheme makes name the active palette here and on the tmux chrome.
func (m *Model) previewTheme(name string) tea.Cmd {
	if err := theme.Load(name); err != nil {
		m.msg = err.Error()
		return nil
	}
	run := m.be.(ThemeBackend).ThemeChrome()
	return func() tea.Msg { run(); return nil }
}

// themeSaved reports the save; a failed save keeps the preview on screen
// but says it was not written.
func (m *Model) themeSaved(msg themeSavedMsg) {
	if msg.err != nil {
		logx.For("ui").Error("save theme", "theme", msg.name, "err", msg.err)
		m.msg, m.msgAt = "theme "+msg.name+" not saved: "+msg.err.Error(), m.now()
		return
	}
	m.msg, m.msgAt = "theme "+msg.name+" saved", m.now()
}

// themeLines is the picker drawn above the tiles: a title with the keys,
// then a window of theme rows around the cursor, each with its swatches.
func (m *Model) themeLines() []string {
	p := m.tp
	idle := lipgloss.NewStyle().Foreground(colIdle)
	out := []string{idle.Render(fmt.Sprintf(" theme %d/%d · ↑↓ preview · ⏎ save · esc keep %s", p.cur+1, len(p.names), p.orig))}
	start := min(max(p.cur-pickRows/2, 0), max(len(p.names)-pickRows, 0))
	for i := start; i < min(start+pickRows, len(p.names)); i++ {
		out = append(out, m.themeRow(p.names[i], i == p.cur, p.names[i] == p.orig))
	}
	return append(out, "")
}

// themeRow is one theme: cursor, its status swatches on its own ground,
// label, and ● on the saved theme.
func (m *Model) themeRow(name string, cur, saved bool) string {
	pl, _ := theme.Get(name)
	sw := lipgloss.NewStyle().Background(lipgloss.Color(pl.Base)).Padding(0, 1)
	var dots strings.Builder
	for _, c := range []string{pl.Working, pl.Waiting, pl.Error, pl.Select, pl.Text} {
		dots.WriteString(sw.Padding(0).Foreground(lipgloss.Color(c)).Render("●"))
	}
	mark, label := "  ", lipgloss.NewStyle().Foreground(colText).Render(pl.Label)
	if cur {
		mark, label = "› ", lipgloss.NewStyle().Foreground(colText).Bold(true).Render(pl.Label)
	}
	row := " " + mark + sw.Render(dots.String()) + " " + label
	if saved {
		row += lipgloss.NewStyle().Foreground(colIdle).Render(" ● saved")
	}
	return row
}
