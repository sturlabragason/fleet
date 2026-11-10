package ui

import (
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/keys"
	"github.com/sturlabragason/fleet/internal/layout"
	"github.com/sturlabragason/fleet/internal/logx"
)

// View renders the header and a card per agent wrapped to the pane. When the
// cards don't all fit it switches to tiers (tiers.go); a pane too short for
// a card row gets one line of chips instead.
func (m *Model) View() (out string) {
	defer func() {
		if r := recover(); r != nil {
			logx.For("ui").Error("panic in View", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			out = fmt.Sprintf("render error: %v (see log)", r)
		}
	}()
	if m.w == 0 {
		return "loading…"
	}
	return zone.Scan(m.fitPane(append([]string{m.header()}, m.body()...)))
}

// body is everything below the header: the help overlay, the tiers, or the
// card grid and its overflow line.
func (m *Model) body() []string {
	if m.help {
		return strings.Split(m.helpOverlay(), "\n")
	}
	if tv, ok := m.tiers(); ok { // past one screen of cards (ADR-32)
		return m.tierLines(tv)
	}
	g := m.grid()
	var lines []string
	for i := 0; i < g.Cards; i += g.Cols {
		var row []string
		for j := i; j < min(i+g.Cols, g.Cards); j++ {
			row = append(row, m.tile(m.agents[j], g.TileW))
		}
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, row...))
	}
	if c := m.chips(m.agents[g.Cards:], g.Cards == 0); c != "" {
		lines = append(lines, c)
	}
	return lines
}

// fitPane fits the lines to the pane (ADR-54): every line is cut to the pane
// width so nothing can wrap, and the frame is padded with blank lines to the
// full pane height so every row is rewritten. Without the padding a frame
// that needs fewer rows than the last one leaves the old rows on screen --
// after four agents close, their tiles and reports stay behind.
func (m *Model) fitPane(blocks []string) string {
	lines := strings.Split(strings.Join(blocks, "\n"), "\n")
	for i, l := range lines {
		if ansi.StringWidth(l) > m.w {
			lines[i] = ansi.Truncate(l, m.w, "")
		}
	}
	for len(lines) < m.h { // bubbletea drops lines past the pane from the top
		lines = append(lines, "")
	}
	// The frame is exactly m.h lines: no trailing newline (a full pane would
	// push the header off the top) and no overflow (bubbletea would drop the
	// top lines, header first). An over-tall frame loses its bottom rows --
	// the "+N hidden" or "↓ more" line -- so body() must keep inside m.h-1.
	return strings.Join(lines[:max(min(len(lines), m.h), 1)], "\n")
}

// helpLines is the hotkey list for the overlay and the rows it has (the
// pane minus the header). Too tall for the pane: two columns.
func (m *Model) helpLines() (lines []string, avail int) {
	avail = max(m.h-1, 1)
	h := keys.HelpStyled(min(m.w, keys.PopupWidth()-2*keys.PopupMargin-2))
	if strings.Count(h, "\n")+1 > avail {
		h = keys.HelpColumns(m.w)
	}
	return strings.Split(h, "\n"), avail
}

// helpMaxOff is the last scroll offset: the window of avail-1 rows (one is
// kept for the "more" row) then ends on the last line.
func (m *Model) helpMaxOff() int {
	lines, avail := m.helpLines()
	if len(lines) <= avail {
		return 0
	}
	return len(lines) - max(avail-1, 1)
}

// scrollHelp moves the overlay by d lines, clamped.
func (m *Model) scrollHelp(d int) {
	m.helpOff = min(max(m.helpOff+d, 0), m.helpMaxOff())
}

// helpOverlay is the hotkey list centred below the header. Still too tall
// for the pane in two columns: it scrolls (↑↓, PgUp/PgDn, wheel) and the
// last row says which way more keys are, so every key can be reached.
func (m *Model) helpOverlay() string {
	lines, avail := m.helpLines()
	if len(lines) > avail {
		off := min(max(m.helpOff, 0), m.helpMaxOff())
		end := min(off+max(avail-1, 1), len(lines))
		var more []string
		if off > 0 {
			more = append(more, "↑ more")
		}
		if end < len(lines) {
			more = append(more, "↓ more")
		}
		more = append(more, "↑↓ scroll")
		lines = append(lines[off:end:end], lipgloss.NewStyle().Foreground(colIdle).Render(strings.Join(more, " · ")))
	}
	bw := 0
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, m.w, "")
		bw = max(bw, ansi.StringWidth(lines[i]))
	}
	for i, l := range lines { // one block width, so Place centres the block, not each line
		lines[i] = l + strings.Repeat(" ", bw-ansi.StringWidth(l))
	}
	return lipgloss.Place(m.w, avail, lipgloss.Center, lipgloss.Center, strings.Join(lines, "\n"))
}

func (m *Model) header() string {
	counts := map[string]int{}
	for _, a := range m.agents {
		counts[a.Status]++
	}
	h := fmt.Sprintf(" fleet · %d agents · %d working · %d waiting · %d error", len(m.agents), counts["working"], counts["waiting"], counts["error"]+counts["stuck"])
	idle := lipgloss.NewStyle().Foreground(colIdle)
	badge := reportBadge(m.reports)
	if by := strings.TrimSpace(m.badgeBy); by != "" && m.reports > 0 {
		badge = "  " + by
	}
	room := m.w - ansi.StringWidth(h) - ansi.StringWidth(badge) - 2
	var hint string
	switch {
	case m.msg != "":
		hint = idle.Render("  " + m.msg)
	case m.target != "":
		// DESIGN.md §5e: name the target and the next action.
		mark := targetMark + " " + m.target
		line := keys.HintWith([]string{mark, "click card replace"}, keys.ScopeTarget, room)
		hint = "  " + targetStyle.Render(mark) + idle.Render(strings.TrimPrefix(line, mark))
	case m.be.WallEnabled():
		hint = idle.Render("  " + keys.HintWith([]string{"no " + targetMark + " pane", "click a wall pane to pick one"}, keys.ScopeNoTarget, room))
	default:
		hint = idle.Render("  " + keys.Hint(keys.ScopeOverview, room))
	}
	return ansi.Truncate(lipgloss.NewStyle().Bold(true).Render(h)+badgeStyle.Render(badge)+hint, m.w, "…")
}

// reportBadge is the unread-report count for the header (ADR-36), "" when
// nothing is unread; it clears as soon as the admiral reads the reports.
func reportBadge(n int) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return "  " + reportMark + " 1 report"
	}
	return fmt.Sprintf("  %s %d reports", reportMark, n)
}

// reportMark opens the unread-report badge, the same glyph the tmux status
// line uses, so one unread batch reads the same in both places.
const reportMark = "✉"

// badgeStyle draws the unread-report badge: the waiting colour, the role the
// status-line badge already wears for the same state. No new role.
var badgeStyle = lipgloss.NewStyle().Foreground(colWaitA).Bold(true)

// line3 is the tile's last line: the latest report while the agent is not
// working (DESIGN §6.7), else the last screen line.
func line3(a *board.Agent, w int) string {
	if r := a.Report; r != nil && a.Status != "working" {
		g, c := reportStyle(r.State)
		return lipgloss.NewStyle().Foreground(c).Render(ansi.Truncate(g+" "+r.State+": "+r.Summary, w, "…"))
	}
	return lipgloss.NewStyle().Foreground(colText).Render(shorten(a.LastLine, w))
}

// cardMark opens the task-card line on a tile (DESIGN §18 #5, ADR-43).
const cardMark = "◇"

// cardLine is the tile's task-card line: the done-check verdict once fleet
// has run it, else the card's goal. "" when the agent has no card or there
// is no room. It wears roles the tile already has: idle for the goal, the
// report colours for the verdict.
func cardLine(a *board.Agent, w int) string {
	c := a.Card
	if c == nil || w < 4 {
		return ""
	}
	if ch := c.Checked; ch != nil {
		g, col := reportStyle("done")
		if !ch.Pass {
			g, col = reportStyle("blocked")
		}
		return lipgloss.NewStyle().Foreground(col).Render(ansi.Truncate(g+" done-check "+ch.Verdict(), w, "…"))
	}
	text := c.Goal
	if text == "" {
		text = c.Check
	}
	if text == "" {
		return ""
	}
	return lipgloss.NewStyle().Foreground(colIdle).Render(ansi.Truncate(cardMark+" "+text, w, "…"))
}

// tileRow4 is the tile's fourth line, shared by the subagent dots and the
// task-card line: dots first, at most half the width when a card wants the
// rest. "" when the agent has neither.
func tileRow4(a *board.Agent, w, frame int) string {
	dotW := w
	if a.Card != nil {
		dotW = w / 2
	}
	dots := subagentRow(a.Subagents, dotW, frame)
	rest := w
	if dots != "" {
		rest -= ansi.StringWidth(dots) + 2
	}
	cl := cardLine(a, rest)
	switch {
	case dots == "":
		return cl
	case cl == "":
		return dots
	}
	return dots + "  " + cl
}

// grid is the card layout for the current pane and agent count.
func (m *Model) grid() layout.TileGrid {
	return m.cfg.Tiles(m.w, m.h-1, len(m.agents))
}

func (m *Model) tile(a *board.Agent, tw int) string {
	col := statusColor(a.Status, m.frame)
	w := tw - 2
	border := lipgloss.RoundedBorder()
	if a.Name == m.sel {
		border = lipgloss.ThickBorder()
	}
	bc := col
	if a.Name == m.sel {
		bc = cursorColor(col)
	}
	wall := ""
	if m.onWall[a.Name] {
		wall = " ▣"
	}
	tgt, tw2 := "", 0 // the ⚑ and ▶ after the name of a pinned / ▶ agent
	if m.pinned[a.Name] {
		tgt, tw2 = pinStyle.Render(" "+pinMark), 2
	}
	if a.Name == m.target {
		tgt, tw2 = tgt+targetStyle.Render(" "+targetMark), tw2+2
	}
	if a.Leader {
		tgt, tw2 = tgt+leadStyle.Render(" "+leadMark), tw2+2
	}
	if a.WithUser(m.now()) {
		tgt, tw2 = tgt+leadStyle.Render(" "+userMark), tw2+2
	}
	noReport := a.NoReportTag(m.now())
	if noReport != "" {
		tgt, tw2 = tgt+noReportStyleOf(noReport).Render(" "+noReportMark), tw2+2
	}
	chip := m.ownerChip(a)
	tw2 += ansi.StringWidth(chip)
	inner := w - 4
	st1 := cursorStyle(lipgloss.NewStyle().Foreground(col).Bold(true), a.Name == m.sel)
	if a.Name == m.sel {
		st1 = st1.Foreground(cursorColor(col))
	}
	l1 := st1.Render(glyph(a.Status, m.frame)+" "+ansi.Truncate(a.Name, inner-4-tw2, "…")) + tgt + st1.Render(wall)
	st := a.Status
	if noReport != "" {
		st += " · no report"
	}
	state := strings.TrimSpace(st + m.queueMark(a.Name) + " " + since(a.StatusSince, m.now()))
	l2 := lipgloss.NewStyle().Foreground(colIdle).Render(metaLineMark(a.Profile, driverMark(a), state, inner))
	if ti, ok := m.teams[a.Name]; ok && ti.Team != "" {
		ts := lipgloss.NewStyle().Foreground(lipgloss.Color(ti.Color))
		l1 = st1.Render(glyph(a.Status, m.frame)+" "+ansi.Truncate(a.Name, inner-4-2-tw2, "…")) + tgt + " " + ts.Render(teamMark)
		if wall != "" {
			l1 += st1.Render(wall)
		}
		tn := ansi.Truncate(ti.Team, 10, "…")
		rest := lipgloss.NewStyle().Foreground(colIdle).Render(" · " + metaLineMark(a.Profile, driverMark(a), state, inner-ansi.StringWidth(tn)-3))
		l2 = ansi.Truncate(ts.Render(tn)+rest, inner, "…")
	}
	l1 = chip + l1
	l3 := line3(a, inner)
	if a.Status == "queued" { // no pane yet: the start of the goal
		l3 = lipgloss.NewStyle().Foreground(colIdle).Render(shorten(a.Goal, inner))
	}
	text := l1 + "\n" + l2 + "\n" + l3
	if l4 := tileRow4(a, inner, m.frame); l4 != "" {
		text += "\n" + l4
	}
	body := lipgloss.NewStyle().Width(inner+2).Padding(0, 1).Render(text)
	t := lipgloss.NewStyle().Border(border).BorderForeground(bc).Width(w).Height(m.cfg.TileH - 2).Render(body)
	return zone.Mark("tile-"+a.Name, t)
}

// chips is the overflow line: how many agents have no card, then a chip for
// each that fits and "+k" for the rest. all is true when no card fits at
// all; the line then holds every agent and only counts what it cuts.
func (m *Model) chips(rest []*board.Agent, all bool) string {
	if len(rest) == 0 {
		return ""
	}
	dim := lipgloss.NewStyle().Foreground(colIdle)
	line := " "
	if !all {
		line += dim.Render(fmt.Sprintf("+%d hidden:", len(rest))) + "  "
	}
	for i, a := range rest {
		cur := a.Name == m.sel
		sc := statusColor(a.Status, m.frame)
		if cur {
			sc = cursorColor(sc)
		}
		st := cursorStyle(lipgloss.NewStyle().Foreground(sc), cur)
		c := m.ownerChip(a) + st.Render(glyph(a.Status, m.frame)+" "+a.Name+m.queueMark(a.Name))
		if m.pinned[a.Name] {
			c += pinStyle.Render(" " + pinMark)
		}
		if a.Name == m.target {
			c += targetStyle.Render(" " + targetMark)
		}
		if a.Leader {
			c += leadStyle.Render(" " + leadMark)
		}
		if a.WithUser(m.now()) {
			c += leadStyle.Render(" " + userMark)
		}
		if ti, ok := m.teams[a.Name]; ok && ti.Team != "" {
			c += " " + lipgloss.NewStyle().Foreground(lipgloss.Color(ti.Color)).Render(teamMark)
		}
		more := ""
		if left := len(rest) - i - 1; left > 0 {
			more = fmt.Sprintf("  +%d", left)
		}
		if ansi.StringWidth(line+c+more) > m.w {
			return ansi.Truncate(line+dim.Render(fmt.Sprintf("+%d", len(rest)-i)), m.w, "")
		}
		line += zone.Mark("tile-"+a.Name, c) + "  "
	}
	return strings.TrimRight(line, " ")
}

// queueMark is " #n" for a queued agent (1 = starts next), else "".
func (m *Model) queueMark(name string) string {
	if n := m.queuePos[name]; n > 0 {
		return fmt.Sprintf(" #%d", n)
	}
	return ""
}

// driverMark follows the profile on the tile's second line: "·acp" for an
// agent on the acp driver (ADR-77), "" for tmux.
func driverMark(a *board.Agent) string {
	if a.ACP() {
		return "·" + board.DriverACP
	}
	return ""
}

// metaLineMark is metaLine with mark kept right after the profile; the
// profile is cut before the mark.
func metaLineMark(profile, mark, state string, w int) string {
	if mark == "" {
		return metaLine(profile, state, w)
	}
	full := profile + mark + " · " + state
	if ansi.StringWidth(full) <= w {
		return full
	}
	if room := w - ansi.StringWidth(mark+" · "+state); room >= 4 {
		return ansi.Truncate(profile, room, "…") + mark + " · " + state
	}
	return metaLine(profile+mark, state, w)
}

// metaLine is "profile · state" in w cells. The state (status and age) is
// kept whole; the profile is cut first.
func metaLine(profile, state string, w int) string {
	full := profile + " · " + state
	if ansi.StringWidth(full) <= w {
		return full
	}
	if room := w - ansi.StringWidth(" · "+state); room >= 4 {
		return ansi.Truncate(profile, room, "…") + " · " + state
	}
	return ansi.Truncate(state, w, "…")
}

// shorten fits a pane's last line into w cells: runs of spaces collapse,
// then brackets go ("53% (105k/200k)" -> "53% 105k/200k"), then it is cut.
func shorten(s string, w int) string {
	s = strings.Join(strings.Fields(s), " ")
	if ansi.StringWidth(s) > w {
		s = strings.Join(strings.Fields(strings.NewReplacer("(", " ", ")", " ", "[", " ", "]", " ").Replace(s)), " ")
	}
	return ansi.Truncate(s, w, "…")
}

// teamMark follows the name of an agent in a team (● is the waiting glyph).
const teamMark = "◆"

// targetMark follows the name of the agent in the ▶ target slot (DESIGN.md §5e).
const targetMark = "▶"

// pinMark follows the name of a pinned agent (DESIGN.md §5e), before ▶ and
// the team mark; one cell wide.
const pinMark = "⚑"

// leadMark follows the name of a squad leader (ADR-53), after ⚑ and ▶ and
// before the team ◆; one cell wide. Its members need no mark of their own:
// a squad is a team, so they already carry ◆ in the squad colour.
const leadMark = "▲"

// userMark follows the name of an agent the user talks to directly: flagged
// interactive, or typed in lately (ADR-61); after ▲, before ◆; one cell.
const userMark = "✎"

// noReportMark follows the name of an agent that went idle mid-task without
// a report (ADR-68), in the waiting colour: it needs a nudge, not a fold.
const noReportMark = "…"

// noReportStyleOf is the … colour for a NoReportTag: waiting for an agent
// that needs a nudge, muted for the user's own agent (ADR-68).
func noReportStyleOf(tag string) lipgloss.Style {
	if tag == "user" {
		return leadStyle
	}
	return noReportStyle
}

func since(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t).Round(time.Second)
	if d >= time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	if d >= time.Minute {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}
