package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/sturlabragason/fleet/internal/status"
)

// Subagent dots (DESIGN.md §4 Subagents, ADR-18).
const subDot = "•"

// subagentDots picks the dots that fit in width (the newest, in start order)
// and their steady colours; hidden is how many didn't fit. Running dots are
// the working colour and never pulse (ADR-81).
func subagentDots(subs []status.Subagent, width int) (cols []lipgloss.Color, hidden int) {
	n := len(subs)
	show := n
	if 2*n-1 > width {
		show = 0
		for k := n - 1; k > 0; k-- {
			if 2*k-1+1+len("+"+strconv.Itoa(n-k)) <= width {
				show = k
				break
			}
		}
	}
	for _, s := range subs[n-show:] {
		switch s.State {
		case status.SubRunning:
			cols = append(cols, colWorking)
		case status.SubFailed:
			cols = append(cols, colError)
		default:
			cols = append(cols, colIdle)
		}
	}
	return cols, n - show
}

// subagentRow renders the dot row; "" when there are no subagents.
func subagentRow(subs []status.Subagent, width int) string {
	if len(subs) == 0 {
		return ""
	}
	cols, hidden := subagentDots(subs, width)
	parts := make([]string, 0, len(cols)+1)
	for _, c := range cols {
		parts = append(parts, lipgloss.NewStyle().Foreground(c).Render(subDot))
	}
	if hidden > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colIdle).Render("+"+strconv.Itoa(hidden)))
	}
	return strings.Join(parts, " ")
}
