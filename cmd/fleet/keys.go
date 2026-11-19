package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sturlabragason/fleet/internal/keys"
	"github.com/sturlabragason/fleet/internal/logo"
)

// cmdKeys prints the hotkey list. --markdown prints the DESIGN.md table;
// --popup runs the tiny TUI shown by the tmux Alt-? popup.
func cmdKeys(args []string) error {
	mode := ""
	if len(args) > 0 {
		mode = args[0]
	}
	switch mode {
	case "":
		fmt.Println(keys.Help(0))
	case "--markdown":
		fmt.Print(keys.Markdown())
	case "--popup":
		_, err := tea.NewProgram(popup{}, tea.WithAltScreen()).Run()
		return err
	default:
		return fmt.Errorf("usage: fleet keys [--markdown|--popup]")
	}
	return nil
}

// popup renders the styled help list and quits on any key. It draws nothing
// until it knows the client size; a client smaller than the popup cuts the
// bottom and right, never the top.
type popup struct{ w, h int }

func (p popup) Init() tea.Cmd { return nil }

func (p popup) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.w, p.h = msg.Width, msg.Height
	case tea.KeyMsg:
		return p, tea.Quit
	}
	return p, nil
}

func (p popup) View() string {
	if p.w <= 0 || p.h <= 0 {
		return ""
	}
	margin := strings.Repeat(" ", keys.PopupMargin)
	lines := logo.Head(strings.Split(keys.HelpStyled(0), "\n"), p.w-keys.PopupMargin, p.h)
	if len(lines) > p.h {
		lines = lines[:p.h]
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(margin+l, p.w, "")
	}
	return strings.Join(lines, "\n")
}
