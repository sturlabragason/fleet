// Package logo is the fleet mark drawn in a terminal: a 1-row line for chrome,
// a 3-row mark for headers and a framed splash for an empty screen. The art
// is generated from the SVG mark's geometry (scripts/build_ascii_logo.py,
// art.go) and coloured through theme roles at render time, so a theme change
// recolours it (docs/style.md §3, ADR-166).
package logo

import (
	"os"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/sturlabragason/fleet/internal/theme"
)

// Size is one of the three drawings.
type Size int

const (
	// Line is "╳━■ fleet": one row, for the status line.
	Line Size = iota
	// Mark is the 3-row mark with the word beside it.
	Mark
	// Splash is the framed mark with the word in half blocks.
	Splash
)

// art is a drawing and, cell for cell, the role that colours it: i ink,
// a accent, w word, f frame, space for blank.
type art struct{ rows, roles []string }

func (s Size) art() art {
	switch s {
	case Splash:
		return splashArt
	case Mark:
		return markArt
	default:
		return lineArt
	}
}

// Width is the drawing's width in cells.
func (s Size) Width() int { return lipgloss.Width(s.art().rows[0]) }

// Height is the drawing's height in rows.
func (s Size) Height() int { return len(s.art().rows) }

// Fit is the largest drawing that fits w×h cells; ok is false when not even
// the line fits.
func Fit(w, h int) (s Size, ok bool) {
	for _, s := range []Size{Splash, Mark, Line} {
		if s.Width() <= w && s.Height() <= h {
			return s, true
		}
	}
	return Line, false
}

// HeadRows is how many rows Head adds: the mark and a blank line.
var HeadRows = Mark.Height() + 1

// Head puts the 3-row mark and a blank line above lines when all of it fits
// w×h; otherwise lines come back as they are, so the mark never costs a row
// of content (the help list, ADR-166).
func Head(lines []string, w, h int) []string {
	if len(lines)+HeadRows > h || Mark.Width() > w {
		return lines
	}
	head := strings.Split(Render(Mark, ASCII(os.Getenv)), "\n")
	return append(append(head, ""), lines...)
}

// ascii maps every glyph the art uses to the closest ASCII.
var ascii = strings.NewReplacer(
	"─", "-", "━", "=", "│", "|", "┃", "|",
	"╭", ".", "╮", ".", "╰", "'", "╯", "'",
	"┏", "+", "┓", "+", "┗", "+", "┛", "+",
	"┼", "+", "┿", "+", "╂", "+",
	"╲", "\\", "╱", "/", "╳", "X",
	"■", "#", "▪", "#",
	"█", "#", "▀", "\"", "▄", ",",
)

// Plain is the drawing as text, every row the same width; asc swaps each
// glyph for ASCII.
func Plain(s Size, asc bool) []string {
	rows := slices.Clone(s.art().rows)
	if asc {
		for i, r := range rows {
			rows[i] = ascii.Replace(r)
		}
	}
	return rows
}

// Render is the drawing coloured by the active theme: ink and frame in Text,
// the blue lane in Select (the one accent, ADR-166), the word in bold Text.
func Render(s Size, asc bool) string {
	roles := s.art().roles
	rows := Plain(s, asc)
	for i, r := range rows {
		rows[i] = paint([]rune(r), []rune(roles[i]))
	}
	return strings.Join(rows, "\n")
}

// paint styles each run of cells that share a role.
func paint(cells, roles []rune) string {
	var b strings.Builder
	for start := 0; start < len(cells); {
		end := start + 1
		for end < len(cells) && roles[end] == roles[start] {
			end++
		}
		b.WriteString(style(roles[start]).Render(string(cells[start:end])))
		start = end
	}
	return b.String()
}

func style(role rune) lipgloss.Style {
	switch role {
	case ' ':
		return lipgloss.NewStyle()
	case 'a':
		return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Select))
	case 'w':
		return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text)).Bold(true)
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))
	}
}

// ASCII reports whether the box glyphs can't be trusted to draw: TERM=dumb,
// or a locale that isn't UTF-8 outside tmux (fleet starts tmux with -u, so
// inside it Unicode always draws).
func ASCII(getenv func(string) string) bool {
	if getenv("TERM") == "dumb" {
		return true
	}
	_, val := Locale(getenv)
	return getenv("TMUX") == "" && !UTF8(val)
}

// Locale is the variable that sets the character set and its value: LC_ALL,
// then LC_CTYPE, then LANG, as the C library reads them.
func Locale(getenv func(string) string) (name, val string) {
	for _, v := range []string{"LC_ALL", "LC_CTYPE"} {
		if s := getenv(v); s != "" {
			return v, s
		}
	}
	return "LANG", getenv("LANG")
}

// UTF8 reports whether a locale value names UTF-8.
func UTF8(locale string) bool {
	l := strings.ToLower(locale)
	return strings.Contains(l, "utf-8") || strings.Contains(l, "utf8")
}
