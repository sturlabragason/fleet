package main

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/theme"
	"github.com/sturlabragason/fleet/internal/tools"
)

// The admiral profile picker (Alt-P, overview P; DESIGN §6.10). It runs in a
// tmux popup, exits on a choice and leaves the swap to `tmux run-shell -b`.

const (
	pickWidth = 44 // popup width, border included
	pickInner = pickWidth - 2
	pickName  = 18 // name column
)

// pickable lists the MCP-capable profiles, sorted.
func pickable(ps map[string]tools.Profile) []string {
	var out []string
	for _, n := range tools.Names(ps) {
		if mcpCapable(ps[n]) {
			out = append(out, n)
		}
	}
	return out
}

// pickPopupArgs is the tmux display-popup that runs the picker.
func pickPopupArgs(f *fleet.Fleet) []string {
	return []string{"display-popup", "-E", "-b", "rounded",
		"-w", strconv.Itoa(pickWidth), "-h", strconv.Itoa(max(len(pickable(f.Profiles)), 1) + 4),
		"-T", " admiral profile ", tools.Quote(f.Exe) + " admiral profile --pick"}
}

// swapShell is the run-shell command the picker schedules.
func swapShell(exe, name string) string {
	return tools.Quote(exe) + " admiral profile " + tools.Quote(name) + " >/dev/null 2>&1"
}

type pickModel struct {
	names  []string
	active string
	cur    int
	chosen string
}

func newPick(names []string, active string) *pickModel {
	m := &pickModel{names: names, active: active}
	for i, n := range names {
		if n == active {
			m.cur = i
		}
	}
	return m
}

func (m *pickModel) Init() tea.Cmd { return nil }

func (m *pickModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch s := k.String(); s {
	case "up", "k":
		if m.cur > 0 {
			m.cur--
		}
	case "down", "j":
		if m.cur < len(m.names)-1 {
			m.cur++
		}
	case "enter":
		if len(m.names) > 0 {
			m.chosen = m.names[m.cur]
		}
		return m, tea.Quit
	case "esc", "q", "ctrl+c":
		return m, tea.Quit
	default:
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= 9 && n <= len(m.names) {
			m.chosen = m.names[n-1]
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *pickModel) View() string {
	var b strings.Builder
	for i, n := range m.names {
		b.WriteString(m.row(i, n) + "\n")
	}
	if len(m.names) == 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted)).Render("  no copilot or claude profiles") + "\n")
	}
	b.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted)).Render("  ⏎ switch · esc cancel"))
	return b.String()
}

func (m *pickModel) row(i int, name string) string {
	base := lipgloss.NewStyle()
	cursor := "  "
	if i == m.cur {
		base = base.Bold(true).Foreground(lipgloss.Color(theme.Select)).Background(lipgloss.Color(theme.Surface0))
		cursor = "❯ "
	}
	label := fmt.Sprintf("%s%d  %-*s ", cursor, i+1, pickName, name)
	if i >= 9 {
		label = fmt.Sprintf("%s   %-*s ", cursor, pickName, name)
	}
	var hint string
	if name == m.active {
		hint = base.Foreground(lipgloss.Color(theme.Supervisor)).Render("●") + base.Foreground(lipgloss.Color(theme.Muted)).Render(" now · restart")
	} else {
		hint = base.Foreground(lipgloss.Color(theme.Muted)).Render("new chat")
	}
	out := base.Render(label) + hint
	if pad := pickInner - ansi.StringWidth(out); pad > 0 {
		out += base.Render(strings.Repeat(" ", pad))
	}
	return out
}

// cmdAdmiralPick runs the picker and schedules the swap in the background.
func cmdAdmiralPick(f *fleet.Fleet, active string) error {
	m := newPick(pickable(f.Profiles), active)
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return err
	}
	if m.chosen == "" {
		return nil
	}
	logx.For("admiral").Info("picked", "profile", m.chosen, "active", active)
	_, err := f.T.Run("run-shell", "-b", swapShell(f.Exe, m.chosen))
	return err
}
