package ui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/layout"
)

// Overview past one screen of cards (DESIGN.md §5e, ADR-32): agents that
// need you or are on the wall keep full cards, the rest get one-line rows,
// idle agents fold into a chip, and the order holds still while you use it.

// foldSel is m.sel while the cursor is on the fold chip; never an agent name.
const foldSel = "\x00fold"

// freezeFor is how long after a key, click, wheel or fold toggle the order
// holds still, so nothing moves under the pointer.
const freezeFor = 3 * time.Second

// Tiers, in display order. Cards go to the leading run below tierRest.
const (
	tierWaiting = iota
	tierError
	tierStuck
	tierWall
	tierRest // working, starting, queued, unknown
	tierIdle // idle and gone: these fold
)

func tierOf(a *board.Agent, onWall bool, now time.Time) int {
	switch a.Status {
	case "waiting":
		return tierWaiting
	case "error":
		return tierError
	case "stuck":
		return tierStuck
	}
	switch {
	case onWall:
		return tierWall
	case a.NoReportTag(now) != "":
		return tierRest // stopped mid-task: it needs a nudge, so it never folds (ADR-68)
	case a.Status == "idle" || a.Status == "gone":
		return tierIdle
	}
	return tierRest
}

// attention is the order for agents new to a tier: by status rank, the
// longest wait first, then by name.
func attention(a, b *board.Agent) bool {
	ra, rb := rank(a.Status), rank(b.Status)
	if ra != rb {
		return ra < rb
	}
	if a.Status == "waiting" && !a.StatusSince.Equal(b.StatusSince) {
		return a.StatusSince.Before(b.StatusSince)
	}
	return a.Name < b.Name
}

func (m *Model) touch() { m.touched = m.now() }

func (m *Model) frozen() bool {
	return !m.touched.IsZero() && m.now().Sub(m.touched) < freezeFor
}

// arrange orders as for display. Agents that stay in their tier keep their
// relative position; new agents and agents changing tier go to the end of
// their tier; team members sit together. While frozen nothing moves: known
// agents keep their place and tier, new ones are appended.
func (m *Model) arrange(as []*board.Agent) []*board.Agent {
	pos := make(map[string]int, len(m.agents))
	for i, a := range m.agents {
		pos[a.Name] = i
	}
	tiers := make(map[string]int, len(as))
	if m.frozen() && len(m.agents) > 0 {
		for _, a := range as {
			t, ok := m.tierOf[a.Name]
			if !ok {
				t = max(tierOf(a, m.onWall[a.Name], m.now()), tierRest) // a row until the freeze ends
			}
			tiers[a.Name] = t
		}
		sort.SliceStable(as, func(i, j int) bool {
			pi, oi := pos[as[i].Name]
			pj, oj := pos[as[j].Name]
			if oi != oj {
				return oi
			}
			return oi && pi < pj
		})
		m.tierOf = tiers
		return as
	}
	for _, a := range as {
		tiers[a.Name] = tierOf(a, m.onWall[a.Name], m.now())
	}
	stays := func(a *board.Agent) (int, bool) {
		p, ok := pos[a.Name]
		t, had := m.tierOf[a.Name]
		return p, ok && had && t == tiers[a.Name]
	}
	sort.SliceStable(as, func(i, j int) bool {
		a, b := as[i], as[j]
		if ta, tb := tiers[a.Name], tiers[b.Name]; ta != tb {
			return ta < tb
		}
		pa, sa := stays(a)
		pb, sb := stays(b)
		if sa != sb {
			return sa
		}
		if sa {
			return pa < pb
		}
		return attention(a, b)
	})
	m.tierOf = tiers
	return m.groupTeams(as)
}

// groupTeams pulls team members together within each tier, at the place of
// the team's first member.
func (m *Model) groupTeams(as []*board.Agent) []*board.Agent {
	out := make([]*board.Agent, 0, len(as))
	for s := 0; s < len(as); {
		e := s
		for e < len(as) && m.tierOf[as[e].Name] == m.tierOf[as[s].Name] {
			e++
		}
		seen := map[string]bool{}
		for i, a := range as[s:e] {
			tm := m.teams[a.Name].Team
			if tm == "" {
				out = append(out, a)
				continue
			}
			if seen[tm] {
				continue
			}
			seen[tm] = true
			for _, b := range as[s+i : e] {
				if m.teams[b.Name].Team == tm {
					out = append(out, b)
				}
			}
		}
		s = e
	}
	return out
}

// tierView is what the overview shows in tier mode.
type tierView struct {
	g      layout.TierGrid
	cards  []*board.Agent
	cells  []string // agent names in row order; foldSel is the fold chip
	folded []string // idle agents the chip folds (shown or not)
	lines  int      // row lines in all
	off    int      // first row line shown
}

// tiers lays out the overview in tier mode; false when every card fits or
// the pane is too short (cards and the chips line, as before).
func (m *Model) tiers() (tv tierView, ok bool) {
	n := len(m.agents)
	if !m.cfg.Tiered(m.w, m.h-1, n) {
		return tv, false
	}
	urgent := 0
	for urgent < n && m.tierOf[m.agents[urgent].Name] < tierRest {
		urgent++
	}
	tv.g = m.cfg.Tiers(m.w, m.h-1, urgent)
	tv.cards = m.agents[:tv.g.Cards]
	rest := m.agents[tv.g.Cards:]
	var keep []string
	for _, a := range rest {
		tv.cells = append(tv.cells, a.Name)
		if m.tierOf[a.Name] == tierIdle {
			tv.folded = append(tv.folded, a.Name)
		} else {
			keep = append(keep, a.Name)
		}
	}
	if len(rest) <= tv.g.Lines*tv.g.Cols {
		tv.folded = nil
	} else if len(tv.folded) > 0 {
		if !m.unfold {
			tv.cells = keep
		}
		tv.cells = append(tv.cells, foldSel)
	}
	tv.lines = (len(tv.cells) + tv.g.Cols - 1) / tv.g.Cols
	tv.off = min(max(m.rowOff, 0), max(tv.lines-tv.g.Lines, 0))
	return tv, true
}

// cellOf is the row cell the cursor is on: the chip stands in for a folded
// agent. -1 when the cursor is on a card or nowhere in the rows.
func (m *Model) cellOf(tv tierView) int {
	for i, c := range tv.cells {
		if c == m.sel || c == foldSel && !m.unfold && slices.Contains(tv.folded, m.sel) {
			return i
		}
	}
	return -1
}

// tierMove moves the cursor: ←→ one card or cell, ↑↓ one row line; ↓ from
// the cards enters the first row line shown, ↑ from the first row line goes
// back to the cards.
func (m *Model) tierMove(tv tierView, k string) {
	cols, nc, nr := tv.g.Cols, len(tv.cards), len(tv.cells)
	ci := slices.IndexFunc(tv.cards, func(a *board.Agent) bool { return a.Name == m.sel })
	ri := m.cellOf(tv)
	pos := ci
	if ri >= 0 {
		pos = nc + ri
	}
	switch {
	case pos < 0:
		pos = 0
	case k == "left" || k == "h":
		pos = max(pos-1, 0)
	case k == "right" || k == "l":
		pos = min(pos+1, nc+nr-1)
	case k == "down" || k == "j":
		switch {
		case ci >= 0 && nr > 0:
			pos = nc + min(tv.off*cols+min(ci, cols-1), nr-1)
		case ri >= 0 && ri+cols < nr:
			pos += cols
		case ri >= 0 && ri/cols < (nr-1)/cols: // the last line is short
			pos = nc + nr - 1
		}
	case k == "up" || k == "k":
		switch {
		case ri >= cols:
			pos -= cols
		case ri >= 0 && nc > 0:
			pos = min(ri, nc-1)
		}
	}
	if pos >= nc+nr {
		return
	}
	if pos < nc {
		m.sel = tv.cards[pos].Name
	} else {
		m.sel = tv.cells[pos-nc]
	}
	m.saveCursor()
	m.reveal()
}

// reveal scrolls the row area so the cursor's row is shown.
func (m *Model) reveal() {
	tv, ok := m.tiers()
	if !ok {
		return
	}
	i := m.cellOf(tv)
	if i < 0 {
		return
	}
	switch l := i / tv.g.Cols; {
	case l < tv.off:
		m.rowOff = l
	case l >= tv.off+tv.g.Lines:
		m.rowOff = l - tv.g.Lines + 1
	default:
		m.rowOff = tv.off
	}
}

// scrollRows moves the row area by d lines, clamped.
func (m *Model) scrollRows(tv tierView, d int) {
	m.rowOff = min(max(tv.off+d, 0), max(tv.lines-tv.g.Lines, 0))
	m.touch()
}

// toggleFold folds or unfolds the idle agents.
func (m *Model) toggleFold() {
	m.unfold = !m.unfold
	m.touch()
}

// hasFold reports whether the fold chip is shown.
func (m *Model) hasFold() bool {
	tv, ok := m.tiers()
	return ok && slices.Contains(tv.cells, foldSel)
}

// selName is the selected agent, "" on the fold chip.
func (m *Model) selName() string {
	if m.sel == foldSel {
		return ""
	}
	return m.sel
}

// tierLines renders tier mode below the header: the card row, then the
// row lines shown, with scroll hints on the edge lines.
func (m *Model) tierLines(tv tierView) []string {
	var lines []string
	if len(tv.cards) > 0 {
		var row []string
		for _, a := range tv.cards {
			row = append(row, m.tile(a, tv.g.TileW))
		}
		lines = strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, row...), "\n")
	}
	cols, w := tv.g.Cols, tv.g.CellW
	end := min(tv.off+tv.g.Lines, tv.lines)
	at := func(i int) string {
		if i < len(tv.cells) {
			return tv.cells[i]
		}
		return ""
	}
	// The hints take the last cell of the edge lines, unless the cursor is there.
	up, down, cur := -1, -1, m.cellOf(tv)
	if end < tv.lines && (end-1)*cols+cols-1 != cur {
		down = (end-1)*cols + cols - 1
	}
	if tv.off > 0 && down != tv.off*cols+cols-1 && tv.off*cols+cols-1 != cur && at(tv.off*cols+cols-1) != foldSel {
		up = tv.off*cols + cols - 1
	}
	count := func(from, to int) (n int) { // agents in cells[from:to] plus a covered cell
		for i := max(from, 0); i < min(to, len(tv.cells)); i++ {
			n += m.agentsIn(tv, tv.cells[i])
		}
		return n
	}
	dim := lipgloss.NewStyle().Foreground(colIdle)
	for l := tv.off; l < end; l++ {
		var slots []string
		for c := 0; c < cols; c++ {
			i := l*cols + c
			switch i {
			case up:
				n := count(0, tv.off*cols) + m.agentsIn(tv, at(i))
				slots = append(slots, dim.Render(fmt.Sprintf("%*s", w, fmt.Sprintf("↑ %d more", n))))
				continue
			case down:
				n := count(end*cols, len(tv.cells)) + m.agentsIn(tv, at(i))
				slots = append(slots, dim.Render(fmt.Sprintf("%*s", w, fmt.Sprintf("↓ %d more", n))))
				continue
			}
			if i >= len(tv.cells) {
				slots = append(slots, strings.Repeat(" ", w))
				continue
			}
			slots = append(slots, m.rowCell(tv, tv.cells[i], w))
		}
		lines = append(lines, strings.TrimRight(strings.Join(slots, strings.Repeat(" ", layout.RowGap)), " "))
	}
	return lines
}

// agentsIn is how many agents a cell stands for: 1, the folded count for a
// folded chip, 0 for none.
func (m *Model) agentsIn(tv tierView, c string) int {
	switch c {
	case "":
		return 0
	case foldSel:
		if m.unfold {
			return 0
		}
		return len(tv.folded)
	}
	return 1
}

// fit cuts or pads s to exactly w cells.
func fit(s string, w int) string {
	s = ansi.Truncate(s, w, "")
	return s + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0))
}

// rowCell is one row: glyph, name, ⚑ ▶ ▲ ✎ ◆ marks and the status age (the
// queue position for a queued agent), as a tile-<name> zone.
func (m *Model) rowCell(tv tierView, name string, w int) string {
	dim := lipgloss.NewStyle().Foreground(colIdle)
	if name == foldSel {
		label := fmt.Sprintf(" +%d idle", len(tv.folded))
		if m.unfold {
			label = " − fold idle"
		}
		st := cursorStyle(dim, m.cellOf(tv) >= 0 && tv.cells[m.cellOf(tv)] == foldSel)
		return zone.Mark("fold", st.Render(fit(label, w)))
	}
	a := m.agents[slices.IndexFunc(m.agents, func(a *board.Agent) bool { return a.Name == name })]
	col := statusColor(a.Status)
	cur := a.Name == m.sel
	if cur {
		col = cursorColor(col)
	}
	// The cursor underlines the whole row but each mark keeps its own colour:
	// ▶ stays Lavender and a team ◆ stays the team colour (ADR-37).
	var row string
	mark := func(st lipgloss.Style, glyph string) { row += cursorStyle(st, cur).Render(" " + glyph) }
	age := since(a.StatusSince, m.now())
	if q := m.queuePos[a.Name]; q > 0 {
		age = fmt.Sprintf("#%d", q)
	}
	age = fmt.Sprintf("%4s", age)
	mw := 2 * m.markCount(a)
	head := " " + glyph(a.Status) + " " + ansi.Truncate(a.Name, max(w-3-mw-ansi.StringWidth(age)-1, 1), "…")
	row = cursorStyle(lipgloss.NewStyle().Foreground(col), cur).Render(head)
	if m.pinned[a.Name] {
		mark(pinStyle, pinMark)
	}
	if a.Name == m.target {
		mark(targetStyle, targetMark)
	}
	if a.Leader {
		mark(leadStyle, leadMark)
	}
	if a.WithUser(m.now()) {
		mark(leadStyle, userMark)
	}
	if tag := a.NoReportTag(m.now()); tag != "" {
		mark(noReportStyleOf(tag), noReportMark)
	}
	if ti, ok := m.teams[a.Name]; ok && ti.Team != "" {
		mark(lipgloss.NewStyle().Foreground(lipgloss.Color(ti.Color)), teamMark)
	}
	pad := strings.Repeat(" ", max(w-ansi.StringWidth(head)-mw-ansi.StringWidth(age), 0))
	return zone.Mark("tile-"+a.Name, fit(row+cursorStyle(dim, cur).Render(pad+age), w))
}

// markCount is how many two-cell marks (⚑ ▶ ▲ ✎ … ◆) follow an agent's name.
func (m *Model) markCount(a *board.Agent) int {
	n := 0
	if m.pinned[a.Name] {
		n++
	}
	if a.Name == m.target {
		n++
	}
	if a.Leader {
		n++
	}
	if a.WithUser(m.now()) {
		n++
	}
	if a.NoReportTag(m.now()) != "" {
		n++
	}
	if ti, ok := m.teams[a.Name]; ok && ti.Team != "" {
		n++
	}
	return n
}
