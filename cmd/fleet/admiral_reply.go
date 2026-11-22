package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/sessions"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/theme"
	"github.com/sturlabragason/fleet/internal/tools"
)

// The Alt-r quote picker (ADR-189): a tmux popup lists the admiral's recent
// replies; ⏎ pastes the chosen one as a "> quote" under a [re #N, time]
// header into the admiral's input box, unsent.

const replyRows = 14 // list rows in the popup

// replyPopupArgsOf is the tmux display-popup that runs the picker for adm.
func replyPopupArgsOf(f *fleet.Fleet, adm string) []string {
	flag := ""
	if adm != supervisor.Main {
		flag = " --name " + tools.Quote(adm)
	}
	return []string{"display-popup", "-E", "-b", "rounded", "-w", "80%", "-h", fmt.Sprint(replyRows + 6), // border 2 + filter, blank, rows, blank, footer
		"-T", " reply to which " + admiralLabel(adm) + " message? ", tools.Quote(f.Exe) + " admiral reply" + flag}
}

type replyModel struct {
	replies []fleet.Quotable
	resolve func(ref string) []fleet.Quotable // todo and report ids, read-only
	note    string                            // shown under the list, e.g. "(from the screen)"
	filter  string
	shown   []fleet.Quotable
	cur     int
	width   int
	chosen  *fleet.Quotable
}

func newReplyModel(replies []fleet.Quotable, resolve func(string) []fleet.Quotable, note string) *replyModel {
	m := &replyModel{replies: replies, resolve: resolve, note: note, width: 80}
	m.refilter()
	return m
}

// refilter shows what the filter names: items an id resolves to first, then
// the replies holding every filter word.
func (m *replyModel) refilter() {
	m.shown = nil
	if m.resolve != nil {
		m.shown = append(m.shown, m.resolve(m.filter)...)
	}
	words := strings.Fields(strings.ToLower(m.filter))
	for _, q := range m.replies {
		hay := strings.ToLower(q.Ref + " " + q.Text)
		ok := true
		for _, w := range words {
			ok = ok && strings.Contains(hay, w)
		}
		if ok {
			m.shown = append(m.shown, q)
		}
	}
	m.cur = min(m.cur, max(len(m.shown)-1, 0))
}

func (m *replyModel) Init() tea.Cmd { return nil }

func (m *replyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyUp, tea.KeyCtrlP:
			m.cur = max(m.cur-1, 0)
		case tea.KeyDown, tea.KeyCtrlN:
			m.cur = min(m.cur+1, max(len(m.shown)-1, 0))
		case tea.KeyEnter:
			if len(m.shown) > 0 {
				m.chosen = &m.shown[m.cur]
			}
			return m, tea.Quit
		case tea.KeyEsc, tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyBackspace:
			if r := []rune(m.filter); len(r) > 0 {
				m.filter = string(r[:len(r)-1])
				m.refilter()
			}
		case tea.KeyRunes, tea.KeySpace:
			m.filter += string(msg.Runes)
			m.refilter()
		}
	}
	return m, nil
}

func (m *replyModel) View() string {
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted))
	var b strings.Builder
	b.WriteString(muted.Render(" filter (words, or a todo/report id): ") + m.filter + "▏\n\n")
	top := max(0, m.cur-replyRows+1)
	for i := top; i < len(m.shown) && i < top+replyRows; i++ {
		b.WriteString(m.row(i) + "\n")
	}
	if len(m.shown) == 0 {
		b.WriteString(muted.Render("  nothing matches") + "\n")
	}
	foot := "  ↑↓ pick · ⏎ quote into the input box · esc cancel"
	if m.note != "" {
		foot += " · " + m.note
	}
	b.WriteString("\n" + muted.Render(foot))
	return b.String()
}

func (m *replyModel) row(i int) string {
	q := m.shown[i]
	style, cursor := lipgloss.NewStyle(), "  "
	if i == m.cur {
		style = style.Bold(true).Foreground(lipgloss.Color(theme.Select)).Background(lipgloss.Color(theme.Surface0))
		cursor = "❯ "
	}
	age := ""
	if !q.T.IsZero() {
		age = sessions.Age(time.Since(q.T))
	}
	head := fmt.Sprintf("%s%-4s %4s  ", cursor, q.Ref, age)
	if len(q.Ref) > 4 { // a resolved todo or report: its ref is the label
		head = fmt.Sprintf("%s%s %s  ", cursor, q.Ref, age)
	}
	line := sessions.OneLine(q.Text, max(m.width-ansi.StringWidth(head)-2, 10))
	return style.Render(head + line)
}

// cmdAdmiralReply runs the picker and pastes the quote into the admiral's
// input box without sending it, then focuses that pane.
func cmdAdmiralReply(f *fleet.Fleet, args []string) error {
	adm, args, err := nameFlag(f, args)
	if err != nil || len(args) > 0 {
		return fmt.Errorf("usage: fleet admiral reply [--name ADMIRAL]")
	}
	if err := refuseExternal(f, adm, "quote into"); err != nil {
		return err
	}
	pane := supervisor.CurrentOf(f.Home, f.T, mainTarget(), adm).PaneID
	if pane == "" {
		return fmt.Errorf("%s has no pane to quote into", admiralLabel(adm))
	}
	replies, fromScreen, err := f.AdmiralReplies(adm)
	if err != nil {
		return err
	}
	note := ""
	if fromScreen {
		note = fleet.LastMessageScreenNote
	}
	m := newReplyModel(replies, func(ref string) []fleet.Quotable { return f.ResolveRef(adm, ref) }, note)
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return fmt.Errorf("reply picker: %w", err)
	}
	if m.chosen == nil {
		return nil
	}
	logx.For("admiral").Info("quote", "admiral", adm, "ref", m.chosen.Ref)
	if err := f.T.Paste(pane, m.chosen.Quote(), false); err != nil {
		return fmt.Errorf("paste quote: %w", err)
	}
	_, err = f.T.Run("select-pane", "-t", pane)
	return err
}
