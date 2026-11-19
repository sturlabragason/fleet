package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/waits"
)

// sectionLook is a section's mark, colour and title in the w tree.
var sectionLook = map[string]struct {
	glyph, title string
	col          lipgloss.Color
}{
	waits.Waiting: {"■", "WAITING ON YOU", colWaitA},
	waits.Running: {"⠋", "RUNNING", colWorking},
	waits.Open:    {"○", "OPEN, NOBODY ON IT", colText},
	waits.Quiet:   {"·", "QUIET", colIdle},
	waits.Paused:  {"‖", "PAUSED", colIdle},
}

// waitsFooter is the footer's key labels, each a button (ADR-168).
var waitsFooter = [][2]string{
	{"enter", "⏎ put in ▶"}, {"space", "space peek"}, {"r", "r answer/resume"}, {"d", "d done"},
	{"p", "p pause"}, {"l", "l let go"}, {"x", "x mark"}, {"L", "L let go marked"}, {"1", "1-4 section"},
}

const dblClick = 400 * time.Millisecond

// waitsLines draws the w view: a title with the section counts, the context
// chips with the mode keys, the rows, and a footer of key buttons or the
// line being typed.
func (m *Model) waitsLines() []string {
	v := m.wv
	dim := lipgloss.NewStyle().Foreground(colIdle)
	top := []string{m.waitsTitle(), m.waitsChips()}
	switch {
	case !v.loaded:
		return append(top, dim.Render(" loading…"))
	case len(v.rows) == 0 && v.flat:
		return append(top, dim.Render(" Nothing waits on you."), m.waitsFoot())
	case len(v.rows) == 0:
		return append(top, dim.Render(" Nothing here."), m.waitsFoot())
	}
	rows := max(m.h-1-len(top)-1, 1)
	v.off = min(max(v.off, v.sel-rows+1), v.sel)
	v.off = max(min(v.off, len(v.rows)-rows), 0)
	lines := top
	for i := v.off; i < min(v.off+rows, len(v.rows)); i++ {
		lines = append(lines, zone.Mark("wrow"+strconv.Itoa(i), m.waitsRow(i)))
	}
	for len(lines) < len(top)+rows {
		lines = append(lines, "")
	}
	return append(lines, m.waitsFoot())
}

// waitsTitle is "Waits on you  tree" and the count of every section.
func (m *Model) waitsTitle() string {
	v := m.wv
	dim := lipgloss.NewStyle().Foreground(colIdle)
	mode := "tree · by section"
	switch {
	case v.flat:
		mode = "flat"
	case v.byCtx:
		mode = "tree · by context"
	}
	head := " " + lipgloss.NewStyle().Bold(true).Foreground(colText).Render("Waits on you") + dim.Render("  "+mode)
	n := map[string]int{}
	items := v.shown()
	for _, it := range items {
		n[it.Section]++
	}
	if w := waits.Only(items, waits.Waiting); len(w) > 0 {
		head += dim.Render(" · oldest " + board.Age(m.now().Sub(w[0].Since)))
	}
	var counts []string
	for _, s := range waits.Sections {
		if n[s] == 0 && s == waits.Paused {
			continue
		}
		l := sectionLook[s]
		counts = append(counts, lipgloss.NewStyle().Foreground(l.col).Render(l.glyph)+fmt.Sprintf(" %d %s", n[s], strings.ToLower(strings.SplitN(l.title, ",", 2)[0])))
	}
	return spread(head, strings.Join(counts, "  "), m.w)
}

// waitsChips is the context chips (click or c to filter) and the mode keys.
func (m *Model) waitsChips() string {
	v := m.wv
	names, n := v.contexts()
	chip := func(name, label string) string {
		st := lipgloss.NewStyle().Foreground(colIdle)
		if v.ctx == name {
			st, label = st.Foreground(colText).Bold(true), "["+label+"]"
		}
		return zone.Mark("wchip:"+name, st.Render(label))
	}
	chips := []string{chip("", fmt.Sprintf("all %d", len(v.all)))}
	for _, c := range names {
		chips = append(chips, chip(c, fmt.Sprintf("%s %d", c, n[c])))
	}
	line := " " + strings.Join(chips, "  ")
	t, g := "T flat", "G by context"
	if v.flat {
		t = "T tree"
	}
	if v.byCtx {
		g = "G by section"
	}
	f := "/ filter"
	if v.filter != "" {
		f = "/" + v.filter
	}
	return spread(line, keyButtons([][2]string{{"/", f}, {"T", t}, {"G", g}, {"esc", "esc tiles"}}), m.w)
}

// waitsFoot is the key buttons, or the filter or let-go reason being typed.
func (m *Model) waitsFoot() string {
	dim := lipgloss.NewStyle().Foreground(colIdle)
	if in := m.wv.input; in != nil {
		what, keys := "filter: ", "  ⏎ keep  esc clear"
		if in.letgo {
			what, keys = fmt.Sprintf("let go %s, reason: ", strings.Join(in.refs, " ")), "  ⏎ let go  esc cancel"
		}
		return " " + what + lipgloss.NewStyle().Foreground(colText).Render(in.text+"█") + dim.Render(keys)
	}
	foot := waitsFooter
	for len(foot) > 1 && ansi.StringWidth(keyButtons(foot))+1 > m.w {
		foot = foot[:len(foot)-1]
	}
	return " " + keyButtons(foot)
}

// keyButtons renders labels as clickable keys, two spaces apart.
func keyButtons(keys [][2]string) string {
	st := lipgloss.NewStyle().Foreground(colIdle)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = zone.Mark("wkey:"+k[0], st.Render(k[1]))
	}
	return strings.Join(parts, "  ")
}

// spread puts left and right on one line w wide; right is dropped when
// both do not fit.
func spread(left, right string, w int) string {
	gap := w - ansi.StringWidth(left) - ansi.StringWidth(right) - 1
	if gap < 2 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// waitsRow draws row i: the cursor, the indent, then the group or item.
func (m *Model) waitsRow(i int) string {
	v, r := m.wv, m.wv.rows[i]
	mark := "  "
	if i == v.sel {
		mark = lipgloss.NewStyle().Foreground(colSelect).Bold(true).Render("› ")
	}
	left := mark + strings.Repeat("  ", r.depth)
	w := max(m.w-1-ansi.StringWidth(left), 10)
	if r.item != nil {
		return left + m.waitsItem(*r.item, i == v.sel, w)
	}
	return left + m.waitsGroup(r, w)
}

func (m *Model) waitsGroup(r wrow, w int) string {
	dim := lipgloss.NewStyle().Foreground(colIdle)
	fold := "▾ "
	if r.folded {
		fold = "▸ "
	}
	count := dim.Render(fmt.Sprintf("  %d", r.n))
	switch r.level {
	case lvlSection:
		l := sectionLook[r.name]
		parts := []string{fold + lipgloss.NewStyle().Bold(true).Foreground(l.col).Render(l.title) + count}
		for _, p := range r.peek[:min(len(r.peek), 3)] {
			parts = append(parts, lipgloss.NewStyle().Foreground(l.col).Render(l.glyph)+" "+p)
		}
		if len(r.peek) > 3 {
			parts = append(parts, dim.Render(fmt.Sprintf("+%d", len(r.peek)-3)))
		}
		return ansi.Truncate(strings.Join(parts, "   "), w, "…")
	case lvlCtx:
		name := r.name
		if name == "" {
			name = "no context"
		}
		return ansi.Truncate(fold+lipgloss.NewStyle().Foreground(colText).Render(name)+count, w, "…")
	}
	s, right := fold+"◆ "+lipgloss.NewStyle().Bold(true).Foreground(colText).Render(r.name), ""
	if a := m.agent(r.name); a != nil {
		if g := strings.TrimSpace(a.Goal); g != "" {
			s += "  " + dim.Render(strings.SplitN(g, "\n", 2)[0])
		}
		right = a.Shown() + "  " + a.Profile
	}
	if r.folded {
		right = fmt.Sprintf("%d items  ", r.n) + right
	}
	return fitRight(s, dim.Render(right), w)
}

// waitsItem is an item row: mark, kind, (source in the flat list), ask,
// then the key that acts on it and its age at the right.
func (m *Model) waitsItem(it waits.Item, cur bool, w int) string {
	dim := lipgloss.NewStyle().Foreground(colIdle)
	l := sectionLook[it.Section]
	glyph := l.glyph
	if m.wv.marked[it.Ref] && it.Kind == waits.Thread {
		glyph = "✓"
	}
	s := lipgloss.NewStyle().Foreground(kindColor(it)).Render(glyph+" "+fmt.Sprintf("%-9s", ansi.Truncate(it.Kind, 9, "…"))) + " "
	if m.wv.flat {
		srcW := min(18, max(8, m.w/6))
		s += fmt.Sprintf("%-*s ", srcW, ansi.Truncate(it.Source, srcW, "…"))
	}
	txt := lipgloss.NewStyle().Foreground(colText)
	if cur {
		txt = txt.Bold(true)
	}
	s += txt.Render(it.Ask)
	act := ""
	switch {
	case it.Answerable():
		act = "● r answer"
	case it.Kind == waits.Thread:
		act = "r resume"
	case it.Todo():
		act = "d done"
	}
	return fitRight(s, dim.Render(fmt.Sprintf("%s  %4s", act, board.Age(m.now().Sub(it.Since)))), w)
}

// fitRight is left cut to leave room for right, right-aligned in w.
func fitRight(left, right string, w int) string {
	rw := ansi.StringWidth(right)
	left = ansi.Truncate(left, max(w-rw-2, 1), "…")
	return left + strings.Repeat(" ", max(w-ansi.StringWidth(left)-rw, 1)) + right
}

// kindColor reuses the status roles: what blocks is the error colour, what
// waits on the user is the waiting colour, the rest its section's colour.
func kindColor(it waits.Item) lipgloss.Color {
	switch {
	case it.Kind == waits.Blocked || it.Kind == "error":
		return colError
	case it.Section == waits.Waiting:
		return colWaitA
	}
	return sectionLook[it.Section].col
}

// waitsMouse: the wheel moves, a click on a group folds it, on an item
// selects it and a second click puts its agent in ▶; chips filter and the
// key labels are buttons.
func (m *Model) waitsMouse(msg tea.MouseMsg) tea.Cmd {
	v := m.wv
	if v.input != nil {
		return nil
	}
	if msg.Action == tea.MouseActionPress {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			v.move(-1)
		case tea.MouseButtonWheelDown:
			v.move(1)
		}
		return nil
	}
	if msg.Action != tea.MouseActionRelease || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	for _, k := range append(waitsFooter, [2]string{"/"}, [2]string{"T"}, [2]string{"G"}, [2]string{"esc"}) {
		if zone.Get("wkey:" + k[0]).InBounds(msg) {
			return m.waitsKey(k[0])
		}
	}
	names, _ := v.contexts()
	for _, c := range append([]string{""}, names...) {
		if zone.Get("wchip:" + c).InBounds(msg) {
			v.ctx = c
			v.rebuild()
			return nil
		}
	}
	for i := v.off; i < len(v.rows); i++ {
		if !zone.Get("wrow" + strconv.Itoa(i)).InBounds(msg) {
			continue
		}
		again := v.click.row == i && m.now().Sub(v.click.at) < dblClick
		v.sel, v.click = i, wclick{row: i, at: m.now()}
		if v.rows[i].item == nil {
			v.toggle(v.rows[i])
			return nil
		}
		if again {
			v.click = wclick{}
			return m.waitsEnter()
		}
		return nil
	}
	return nil
}
