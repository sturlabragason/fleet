package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/sturlabragason/fleet/internal/theme"
)

// Colours per status, all steady: nothing on the overview blinks or
// animates (ADR-81). They are rebuilt
// whenever the active palette changes, because a lipgloss colour is a value,
// not a reference (ADR-55).
var (
	colWorking lipgloss.Color
	colWaitA   lipgloss.Color
	colError   lipgloss.Color
	colStuck   lipgloss.Color
	colIdle    lipgloss.Color
	colGone    lipgloss.Color
	colSelect  lipgloss.Color
	colText    lipgloss.Color

	leadStyle     lipgloss.Style // leadMark: Overlay1, like ⚑, so no new colour role
	pinStyle      lipgloss.Style // pinMark: Overlay1, never the select colour
	targetStyle   lipgloss.Style // targetMark: always the select colour, bold
	noReportStyle lipgloss.Style // noReportMark: the waiting colour
)

func init() { theme.OnChange(applyTheme) }

func applyTheme() {
	colWorking = lipgloss.Color(theme.Working)
	colWaitA = lipgloss.Color(theme.Waiting)
	colError = lipgloss.Color(theme.Error)
	colStuck = lipgloss.Color(theme.Stuck)
	colIdle = lipgloss.Color(theme.Idle)
	colGone = lipgloss.Color(theme.Gone)
	colSelect = lipgloss.Color(theme.Select)
	colText = lipgloss.Color(theme.Text)

	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Overlay1))
	leadStyle, pinStyle = dim, dim
	targetStyle = lipgloss.NewStyle().Foreground(colSelect).Bold(true)
	noReportStyle = lipgloss.NewStyle().Foreground(colWaitA)
}

func statusColor(st string) lipgloss.Color {
	switch st {
	case "working":
		return colWorking
	case "waiting":
		return colWaitA
	case "error":
		return colError
	case "stuck":
		return colStuck
	case "gone", "queued", "parked":
		return colGone
	case "starting":
		return colWorking
	}
	return colIdle
}

// glyph is one static mark per status, like Copilot CLI's own "○ Working":
// the colour tells working (○) from idle (○), never motion (ADR-81).
func glyph(st string) string {
	switch st {
	case "working":
		return "○"
	case "waiting":
		return "●"
	case "error":
		return "✖"
	case "stuck":
		return "⧗"
	case "idle":
		return "○"
	case "gone":
		return "·"
	case "queued":
		return "‖"
	case "parked":
		return "z"
	case "starting":
		return "▸"
	}
	return "?"
}

// reportStyle is the glyph and colour of a report state on a tile.
func reportStyle(state string) (string, lipgloss.Color) {
	switch state {
	case "done":
		return "✓", colWorking
	case "blocked":
		return "✗", colError
	case "question":
		return "?", colWaitA
	}
	return "…", colIdle
}

// attention rank: lower sorts first.
func rank(st string) int {
	switch st {
	case "waiting":
		return 0
	case "error":
		return 1
	case "stuck":
		return 2
	case "working", "starting":
		return 3
	case "idle":
		return 4
	case "unknown":
		return 5
	case "queued":
		return 7
	case "parked":
		return 8
	}
	return 6
}

// cursorStyle marks the overview cursor (ADR-37): an underline, in whatever
// colour the cell already carries, which for an agent is its status colour.
// Lavender (colSelect) is left to the ▶ target mark alone, so the two states
// never look alike.
func cursorStyle(s lipgloss.Style, on bool) lipgloss.Style {
	if !on {
		return s
	}
	return s.Underline(true)
}

// cursorColor is the ink of a cursor cell: the status colour, except for the
// statuses that are meant to recede. Surface2 (gone, queued) is 2.5:1 on the
// base, so an underline drawn in it would be invisible; those cells take the
// plain bright Text instead. No new role either way.
func cursorColor(c lipgloss.Color) lipgloss.Color {
	if c == colGone {
		return colText
	}
	return c
}
