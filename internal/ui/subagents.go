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
// and their colours for this animation frame; hidden is how many didn't fit.
// Running dots carry a wave: one step per 3 frames (300 ms), every third
// running dot lit, so even a single running dot pulses.
func subagentDots(subs []status.Subagent, width, frame int) (cols []lipgloss.Color, hidden int) {
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
	step := (frame / 3) % 3
	ri := 0
	for _, s := range subs[n-show:] {
		switch s.State {
		case status.SubRunning:
			c := colWorkingDim
			if ri%3 == step {
				c = colWorking
			}
			ri++
			cols = append(cols, c)
		case status.SubFailed:
			cols = append(cols, colError)
		default:
			cols = append(cols, colIdle)
		}
	}
	return cols, n - show
}

// subagentRow renders the dot row; "" when there are no subagents. Only the
// colours change between frames, so the row keeps its width.
func subagentRow(subs []status.Subagent, width, frame int) string {
	if len(subs) == 0 {
		return ""
	}
	cols, hidden := subagentDots(subs, width, frame)
	parts := make([]string, 0, len(cols)+1)
	for _, c := range cols {
		parts = append(parts, lipgloss.NewStyle().Foreground(c).Render(subDot))
	}
	if hidden > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colIdle).Render("+"+strconv.Itoa(hidden)))
	}
	return strings.Join(parts, " ")
}
