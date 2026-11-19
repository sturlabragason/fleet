package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/waits"
)

// WaitsBackend is what the w view and the r answer view need (ADR-167).
// Optional, so a backend without it says so instead.
type WaitsBackend interface {
	Waits() []waits.Item
	Pending(name string) (fleet.Pending, error)
	Reply(name string, r fleet.Reply, expect string) (string, error)
}

// waitsView is the "waits on you" list that replaces the tiles while open.
type waitsView struct {
	items  []waits.Item
	loaded bool
	sel    int
	off    int
}

type waitsMsg []waits.Item

// openWaits is w: open the list and load it.
func (m *Model) openWaits() tea.Cmd {
	if _, ok := m.be.(WaitsBackend); !ok {
		m.msg = "w needs fleet's backend"
		return nil
	}
	m.wv = &waitsView{}
	return m.loadWaits()
}

// loadWaits reads the list off the UI loop; each poll calls it while the
// view is open, so it stays as fresh as the tiles.
func (m *Model) loadWaits() tea.Cmd {
	wb, ok := m.be.(WaitsBackend)
	if !ok {
		return nil
	}
	return func() tea.Msg { return waitsMsg(wb.Waits()) }
}

// setWaits keeps the cursor on the same item when the list changes.
func (v *waitsView) set(items []waits.Item) {
	var cur waits.Item
	if v.sel < len(v.items) {
		cur = v.items[v.sel]
	}
	v.items, v.loaded = items, true
	v.sel = min(v.sel, max(len(items)-1, 0))
	for i, it := range items {
		if it.Kind == cur.Kind && it.Source == cur.Source && it.Ask == cur.Ask {
			v.sel = i
			break
		}
	}
}

func (m *Model) waitsKey(k string) tea.Cmd {
	v := m.wv
	switch k {
	case "esc", "q", "w":
		m.wv = nil
	case "ctrl+c":
		return tea.Quit
	case "up", "k":
		v.sel = max(v.sel-1, 0)
	case "down", "j":
		v.sel = min(v.sel+1, max(len(v.items)-1, 0))
	case "home", "g":
		v.sel = 0
	case "end", "G":
		v.sel = max(len(v.items)-1, 0)
	case "enter", "r":
		if v.sel >= len(v.items) {
			return nil
		}
		it := v.items[v.sel]
		if it.Agent == "" || m.agent(it.Agent) == nil {
			m.msg = it.Source + " is not an agent on the board"
			return nil
		}
		m.sel = it.Agent
		m.saveCursor()
		if k == "r" {
			return m.openAnswer(it.Agent)
		}
		m.wv = nil
		m.reveal()
		return m.show()
	}
	return nil
}

func (m *Model) agent(name string) *board.Agent {
	for _, a := range m.agents {
		if a.Name == name {
			return a
		}
	}
	return nil
}

// waitsLines draws the list: a title line, a column header, then one dense
// row per item (age, kind, source, ask), oldest first.
func (m *Model) waitsLines() []string {
	v := m.wv
	dim := lipgloss.NewStyle().Foreground(colIdle)
	title := lipgloss.NewStyle().Bold(true).Foreground(colText)
	head := fmt.Sprintf(" %s  %d", title.Render("Waits on you"), len(v.items))
	if len(v.items) > 0 {
		head += dim.Render(" · oldest " + board.Age(m.now().Sub(v.items[0].Since)))
	}
	keysHint := "⏎ show · r answer · ↑↓ move · esc close"
	head += strings.Repeat(" ", max(2, m.w-ansi.StringWidth(head)-ansi.StringWidth(keysHint)-1)) + dim.Render(keysHint)
	lines := []string{head}
	switch {
	case !v.loaded:
		return append(lines, dim.Render(" loading…"))
	case len(v.items) == 0:
		return append(lines, dim.Render(" Nothing waits on you."))
	}
	const ageW, kindW = 5, 10
	srcW := min(18, max(8, m.w/6))
	lines = append(lines, dim.Render(fmt.Sprintf("  %-*s %-*s %-*s %s", ageW, "AGE", kindW, "KIND", srcW, "SOURCE", "ASK")))
	rows := max(m.h-1-len(lines), 1)
	v.off = min(max(v.off, v.sel-rows+1), v.sel)
	v.off = max(min(v.off, len(v.items)-rows), 0)
	for i := v.off; i < min(v.off+rows, len(v.items)); i++ {
		it := v.items[i]
		mark, txt := "  ", lipgloss.NewStyle().Foreground(colText)
		if i == v.sel {
			mark = lipgloss.NewStyle().Foreground(colSelect).Bold(true).Render("› ")
			txt = txt.Bold(true)
		}
		src := it.Source
		if it.Answerable() {
			src += " ●"
		}
		row := mark + dim.Render(fmt.Sprintf("%-*s ", ageW, board.Age(m.now().Sub(it.Since)))) +
			lipgloss.NewStyle().Foreground(kindColor(it.Kind)).Render(fmt.Sprintf("%-*s ", kindW, ansi.Truncate(it.Kind, kindW, "…"))) +
			fmt.Sprintf("%-*s ", srcW, ansi.Truncate(src, srcW, "…")) + txt.Render(it.Ask)
		lines = append(lines, row)
	}
	return lines
}

// kindColor reuses the status roles: what blocks is the error colour, what
// asks is the waiting colour, the rest is muted text.
func kindColor(k string) lipgloss.Color {
	switch k {
	case waits.Blocked:
		return colError
	case waits.Prompt, waits.Question, waits.NoReport, "ask":
		return colWaitA
	case waits.Report, "promise", "request":
		return colText
	}
	return colIdle
}
