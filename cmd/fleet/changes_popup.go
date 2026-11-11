package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sturlabragason/fleet/internal/keys"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/theme"
	"github.com/sturlabragason/fleet/internal/whatsnew"
)

// Row geometry of the what's new popup (DESIGN.md §5d).
const (
	wnDateW   = 10 // YYYY-MM-DD
	wnGap     = 2
	wnIndent  = wnDateW + wnGap + 1 + wnGap // where the text starts
	wnMaxText = 100
	wnWheel   = 3
)

// changesPopup is the scrollable what's new list behind Alt-g. It has no
// selection; it scrolls by line.
type changesPopup struct {
	w, h     int
	header   string
	entries  []whatsnew.Entry
	isNew    map[string]bool
	markSeen func([]whatsnew.Entry) error
	top      int
	lines    []string // rendered body for linesW
	linesW   int
}

func newChangesPopup(version string, es []whatsnew.Entry, isNew map[string]bool, markSeen func([]whatsnew.Entry) error) *changesPopup {
	return &changesPopup{header: whatsnew.Header(version, es, isNew), entries: es, isNew: isNew, markSeen: markSeen}
}

func (p *changesPopup) Init() tea.Cmd { return nil }

// markAll records every listed entry as seen. It runs after the program
// exits, so a quick close can't skip it; the marks drawn came from the seen
// file before that.
func (p *changesPopup) markAll() {
	if p.markSeen == nil {
		return
	}
	if err := p.markSeen(p.entries); err != nil {
		logx.For("whatsnew").Error("mark seen", "err", err)
	}
}

func fg(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c)) }

// bodyH is the number of rows below the header and its blank line.
func (p *changesPopup) bodyH() int { return max(1, p.h-2) }

func (p *changesPopup) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.w, p.h = msg.Width, msg.Height
		p.render()
		p.scroll(0)
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c", "g", "alt+g":
			return p, tea.Quit
		case "up", "k":
			p.scroll(-1)
		case "down", "j":
			p.scroll(1)
		case "pgup":
			p.scroll(-p.page())
		case "pgdown":
			p.scroll(p.page())
		case "home":
			p.top = 0
		case "end":
			p.scroll(len(p.lines))
		}
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			p.scroll(-wnWheel)
		case tea.MouseButtonWheelDown:
			p.scroll(wnWheel)
		}
	}
	return p, nil
}

func (p *changesPopup) page() int { return max(1, p.bodyH()-1) }

// scroll moves the view by d lines and keeps it within the body.
func (p *changesPopup) scroll(d int) {
	p.top = max(0, min(p.top+d, len(p.lines)-p.bodyH()))
}

func (p *changesPopup) View() string {
	if p.w <= 0 || p.h <= 0 {
		return ""
	}
	lines := []string{fg(theme.Overlay1).Render(p.header), ""}
	body := p.lines
	if len(p.entries) == 0 {
		body = []string{fg(theme.Overlay1).Render("no entries")}
	}
	bh := p.bodyH()
	end := min(len(body), p.top+bh)
	if end < len(body) {
		lines = append(lines, body[p.top:end-1]...)
		lines = append(lines, fg(theme.Overlay0).Render("↓ more"))
	} else {
		lines = append(lines, body[min(p.top, end):end]...)
	}
	if len(lines) > p.h {
		lines = lines[:p.h]
	}
	margin := strings.Repeat(" ", keys.PopupMargin)
	for i, l := range lines {
		if l == "" {
			continue
		}
		lines[i] = ansi.Truncate(margin+l, p.w, "")
	}
	return strings.Join(lines, "\n")
}

// render lays the entries out for the current width.
func (p *changesPopup) render() {
	cw := max(1, p.w-2*keys.PopupMargin)
	if p.lines != nil && p.linesW == cw {
		return
	}
	tw := max(1, min(cw-wnIndent, wnMaxText))
	indent := strings.Repeat(" ", wnIndent)
	gap := strings.Repeat(" ", wnGap)
	var out []string
	day := ""
	for i, e := range p.entries {
		date := strings.Repeat(" ", wnDateW)
		if e.Date != day {
			if i > 0 {
				out = append(out, "")
			}
			date, day = fg(theme.Overlay1).Render(e.Date), e.Date
		}
		mark := " "
		if p.isNew[e.ID()] {
			mark = fg(theme.Text).Bold(true).Render(whatsnew.NewMark)
		}
		for j, l := range wrapEntry(e, tw) {
			if j == 0 {
				out = append(out, date+gap+mark+gap+l)
			} else {
				out = append(out, indent+l)
			}
		}
	}
	p.lines, p.linesW = out, cw
}

// cell is one character of entry text; code marks a `code span`.
type cell struct {
	s    string
	code bool
}

// words splits Markdown text into words of cells, dropping backticks.
func words(text string) [][]cell {
	var ws [][]cell
	var cur []cell
	code := false
	for _, r := range text {
		switch r {
		case '`':
			code = !code
		case ' ':
			if len(cur) > 0 {
				ws = append(ws, cur)
				cur = nil
			}
		default:
			cur = append(cur, cell{string(r), code})
		}
	}
	if len(cur) > 0 {
		ws = append(ws, cur)
	}
	return ws
}

func cellsW(cs []cell) int {
	n := 0
	for _, c := range cs {
		n += ansi.StringWidth(c.s)
	}
	return n
}

// wrapEntry wraps the entry's text to tw cells and adds its refs as one
// unbreakable token, on a line of their own when they don't fit.
func wrapEntry(e whatsnew.Entry, tw int) []string {
	var lines [][]cell
	var cur []cell
	for _, w := range words(e.Text) {
		ww := cellsW(w)
		switch {
		case len(cur) == 0:
		case cellsW(cur)+1+ww <= tw:
			cur = append(cur, cell{" ", false})
		default:
			lines, cur = append(lines, cur), nil
		}
		for ww > tw && len(cur) == 0 { // a word wider than the column
			n, k := 0, 0
			for k < len(w) && n+ansi.StringWidth(w[k].s) <= tw {
				n += ansi.StringWidth(w[k].s)
				k++
			}
			k = max(1, k)
			lines = append(lines, w[:k])
			w = w[k:]
			ww = cellsW(w)
		}
		cur = append(cur, w...)
	}
	if len(cur) > 0 || len(lines) == 0 {
		lines = append(lines, cur)
	}
	base, code := fg(theme.Text), fg(theme.Text).Bold(true)
	if e.Commit {
		base, code = fg(theme.Overlay1), fg(theme.Overlay1).Bold(true)
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = renderCells(l, base, code)
	}
	if len(e.Refs) > 0 {
		refs := "(" + strings.Join(e.Refs, ", ") + ")"
		r := fg(theme.Overlay0).Render(refs)
		if last := len(out) - 1; cellsW(lines[last])+1+ansi.StringWidth(refs) <= tw {
			out[last] += " " + r
		} else {
			out = append(out, r)
		}
	}
	return out
}

// renderCells styles runs of plain and code cells.
func renderCells(cs []cell, base, code lipgloss.Style) string {
	var b strings.Builder
	for i := 0; i < len(cs); {
		j := i
		var run strings.Builder
		for j < len(cs) && cs[j].code == cs[i].code {
			run.WriteString(cs[j].s)
			j++
		}
		st := base
		if cs[i].code {
			st = code
		}
		b.WriteString(st.Render(run.String()))
		i = j
	}
	return b.String()
}
