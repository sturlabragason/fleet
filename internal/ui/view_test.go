package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/charmbracelet/lipgloss"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/status"
)

// overview renders n agents in a w×h pane with the last one selected.
func overview(w, h, n int) (*Model, string) {
	now := time.Now()
	m := New(&fakeBE{}, "")
	m.now = func() time.Time { return now }
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	var as []*board.Agent
	for i := 0; i < n; i++ {
		as = append(as, &board.Agent{Name: fmt.Sprintf("agent-%02d", i), Profile: "claude-copilot",
			Status: "working", LastLine: "53% (105k/200k)", StatusSince: now.Add(-12 * time.Minute)})
	}
	m.setAgents(as)
	m.sel = as[n-1].Name
	m.reveal()
	return m, ansi.Strip(m.View())
}

func cards(v string) int { return strings.Count(v, "╭") + strings.Count(v, "┏") }

func TestOverviewFitsEveryPane(t *testing.T) {
	zone.NewGlobal()
	for _, sz := range [][2]int{{150, 13}, {150, 40}, {80, 13}, {60, 9}, {200, 20}, {150, 4}} {
		for _, n := range []int{1, 5, 6, 9, 12, 20} {
			w, h := sz[0], sz[1]
			m, v := overview(w, h, n)
			lines := strings.Split(v, "\n") // as bubbletea counts: a trailing \n is a line
			if len(lines) > h {
				t.Errorf("%dx%d n=%d: %d lines", w, h, n, len(lines))
			}
			if !strings.HasPrefix(lines[0], " fleet · ") {
				t.Errorf("%dx%d n=%d: header missing: %q", w, h, n, lines[0])
			}
			for _, l := range lines {
				if ansi.StringWidth(l) > w {
					t.Errorf("%dx%d n=%d: line wider than pane: %q", w, h, n, l)
				}
			}
			g := m.grid()
			if tv, ok := m.tiers(); ok { // past one screen of cards: rows (ADR-32)
				if got := cards(v); got != tv.g.Cards {
					t.Errorf("%dx%d n=%d: %d cards, tiers say %d:\n%s", w, h, n, got, tv.g.Cards, v)
				}
				if !strings.Contains(v, "⠋ "+m.sel) || strings.Contains(v, "hidden") {
					t.Errorf("%dx%d n=%d: selected %s not shown as a row, or a chips line:\n%s", w, h, n, m.sel, v)
				}
				if strings.Count(v, "agent-") < n && !strings.Contains(v, "more") {
					t.Errorf("%dx%d n=%d: agents missing without a hint:\n%s", w, h, n, v)
				}
				continue
			}
			if got := cards(v); got != g.Cards {
				t.Errorf("%dx%d n=%d: %d cards, grid says %d:\n%s", w, h, n, got, g.Cards, v)
			}
			hidden := n - g.Cards
			switch {
			case hidden == 0:
				if strings.Contains(v, "hidden") {
					t.Errorf("%dx%d n=%d: all fit but says hidden:\n%s", w, h, n, v)
				}
			case g.Cards > 0:
				if !strings.Contains(v, fmt.Sprintf("+%d hidden:", hidden)) {
					t.Errorf("%dx%d n=%d: want +%d hidden:\n%s", w, h, n, hidden, v)
				}
				if !strings.Contains(v, "┃ ⠋ "+m.sel) {
					t.Errorf("%dx%d n=%d: selected %s has no card:\n%s", w, h, n, m.sel, v)
				}
			}
		}
	}
}

// The reported bug: 9 agents in a ~150-column, 13-row overview drew 5 cards
// and a line of bare chips. All 9 must get a card.
func TestOverviewNineAgentsAllCards(t *testing.T) {
	zone.NewGlobal()
	_, v := overview(150, 13, 9)
	if got := cards(v); got != 9 {
		t.Fatalf("want 9 cards, got %d:\n%s", got, v)
	}
	if strings.Contains(v, "hidden") || strings.Contains(v, "wo…") {
		t.Errorf("no overflow line and no cut status expected:\n%s", v)
	}
	for _, want := range []string{"agent-00", "agent-08", "working 12m", "53% (105k/200k)"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
}

// trimBlank drops the blank padding rows fitPane adds to fill the pane.
func trimBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// The reported bug: four of six agents closed at once and the overview kept
// the closed agents' tiles, their reports and half-drawn borders on the rows
// the smaller grid no longer uses. A frame must fill the pane, so every row
// of the last frame is rewritten, and no line may wrap.
func TestOverviewLeavesNoStaleRows(t *testing.T) {
	zone.NewGlobal()
	for _, sz := range [][2]int{{289, 13}, {150, 14}, {130, 20}, {80, 13}} {
		w, h := sz[0], sz[1]
		for _, n := range []int{6, 7, 9, 20} {
			big, before := overview(w, h, n)
			if got := strings.Count(before, "\n") + 1; got != h {
				t.Errorf("%dx%d n=%d: frame is %d rows, pane is %d", w, h, n, got, h)
			}
			big.setAgents(big.agents[:2]) // four (or more) agents close at once
			big.sel = big.agents[1].Name
			after := ansi.Strip(big.View())
			lines := strings.Split(after, "\n")
			if len(lines) != h {
				t.Errorf("%dx%d n=%d->2: frame is %d rows, pane is %d", w, h, n, len(lines), h)
			}
			for _, l := range lines {
				if ansi.StringWidth(l) > w {
					t.Errorf("%dx%d n=%d->2: line wraps the pane (%d cells): %q", w, h, n, ansi.StringWidth(l), l)
				}
			}
			for _, a := range []string{"agent-02", "agent-05"} { // closed agents
				if strings.Contains(after, a) {
					t.Errorf("%dx%d n=%d->2: %s still drawn:\n%s", w, h, n, a, after)
				}
			}
			if got, want := cards(after), 2; got != want && !strings.Contains(after, "hidden") {
				t.Errorf("%dx%d n=%d->2: %d cards, want %d:\n%s", w, h, n, got, want, after)
			}
			// the visible symptom: bottom borders of tiles the new grid no longer draws
			if got := strings.Count(after, "╰") + strings.Count(after, "┗"); got != cards(after) {
				t.Errorf("%dx%d n=%d->2: %d bottom borders for %d cards:\n%s", w, h, n, got, cards(after), after)
			}
		}
	}
}

// fitPane is the last guard before the pane: a block that measures wider
// than the pane (a glyph the width tables and the terminal disagree about,
// a tile that outgrows the width the grid gave it) is cut rather than left
// to wrap, and a short frame is filled out to the pane height.
func TestFitPaneCutsAndFills(t *testing.T) {
	zone.NewGlobal()
	m, _ := overview(80, 10, 1)
	got := strings.Split(m.fitPane([]string{strings.Repeat("x", 100), "a\nb"}), "\n")
	if len(got) != 10 {
		t.Fatalf("want 10 rows, got %d: %q", len(got), got)
	}
	if ansi.StringWidth(got[0]) != 80 {
		t.Errorf("row 0 is %d cells, want 80", ansi.StringWidth(got[0]))
	}
	if got[1] != "a" || got[2] != "b" { // a multi-line block counts as its lines
		t.Errorf("rows 1,2 = %q,%q, want \"a\",\"b\"", got[1], got[2])
	}
	for i, l := range got[3:] {
		if l != "" {
			t.Errorf("row %d not blank: %q", i+3, l)
		}
	}
}

func TestChipsCountWhatTheyCut(t *testing.T) {
	zone.NewGlobal()
	_, v := overview(60, 3, 20) // no room for a card
	last := strings.Split(strings.TrimRight(v, "\n"), "\n")[1]
	shown := strings.Count(last, "agent-")
	if want := fmt.Sprintf("+%d", 20-shown); !strings.HasSuffix(last, want) {
		t.Errorf("chips line %q should end with %s", last, want)
	}
}

func TestShortenAndMeta(t *testing.T) {
	for _, tc := range []struct {
		in   string
		w    int
		want string
	}{
		{"53% (105k/200k)", 20, "53% (105k/200k)"},
		{"53% (105k/200k)", 13, "53% 105k/200k"},
		{"53% (105k/200k)", 8, "53% 105…"},
		{"  a   b  ", 10, "a b"},
	} {
		if got := shorten(tc.in, tc.w); got != tc.want {
			t.Errorf("shorten(%q,%d) = %q, want %q", tc.in, tc.w, got, tc.want)
		}
	}
	for _, tc := range []struct {
		w    int
		want string
	}{
		{40, "claude-copilot · working 12m"},
		{26, "claude-copi… · working 12m"},
		{16, "working 12m"},
		{8, "working…"},
	} {
		if got := metaLine("claude-copilot", "working 12m", tc.w); got != tc.want {
			t.Errorf("metaLine(w=%d) = %q, want %q", tc.w, got, tc.want)
		}
	}
}

func TestQueuedTileShowsPositionAndGoal(t *testing.T) {
	zone.NewGlobal()
	now := time.Now()
	m := New(&fakeBE{}, "")
	m.now = func() time.Time { return now }
	m.Update(tea.WindowSizeMsg{Width: 150, Height: 20})
	m.setAgents([]*board.Agent{
		{Name: "w1", Profile: "fake", Status: "working", StatusSince: now},
		{Name: "q2", Profile: "fake", Status: "queued", Goal: "second job", Created: now, StatusSince: now.Add(-3 * time.Minute)},
		{Name: "q1", Profile: "fake", Status: "queued", Goal: "first job", Created: now.Add(-time.Minute), StatusSince: now.Add(-3 * time.Minute)},
		{Name: "s1", Profile: "fake", Status: "starting", StatusSince: now.Add(-8 * time.Second)},
	})
	var order []string
	for _, a := range m.agents {
		order = append(order, a.Name)
	}
	if strings.Join(order, ",") != "s1,w1,q1,q2" { // starting ranks with working, queued last
		t.Errorf("sort: %v", order)
	}
	tw := m.grid().TileW
	q1 := ansi.Strip(m.tile(m.agents[2], tw))
	for _, want := range []string{"‖ q1", "fake · queued #1 3m", "first job"} {
		if !strings.Contains(q1, want) {
			t.Errorf("queued tile missing %q:\n%s", want, q1)
		}
	}
	for _, l := range strings.Split(q1, "\n") {
		if ansi.StringWidth(l) != tw {
			t.Errorf("row width %d, want %d: %q", ansi.StringWidth(l), tw, l)
		}
	}
	if s := ansi.Strip(m.tile(m.agents[0], tw)); !strings.Contains(s, "▸ s1") || !strings.Contains(s, "starting 8s") {
		t.Errorf("starting tile:\n%s", s)
	}
	if c := ansi.Strip(m.chips(m.agents[2:3], false)); !strings.Contains(c, "‖ q1 #1") {
		t.Errorf("chip: %q", c)
	}
}

// The cursor is an underline in the status colour; Lavender (theme.Select)
// is left to the \u25b6 target mark alone (ADR-37).
func TestCursorIsUnderlineNotSelectColour(t *testing.T) {
	base := lipgloss.NewStyle().Foreground(colWorking)
	cur := cursorStyle(base, true)
	if !cur.GetUnderline() {
		t.Error("the cursor must be an underline")
	}
	if got := cur.GetForeground(); got != colWorking {
		t.Errorf("the cursor keeps the status colour, got %v", got)
	}
	if cur.GetReverse() {
		t.Error("the cursor must not use reverse video")
	}
	// Statuses that are meant to recede (gone, queued: Surface2, 2.5:1) would
	// make the underline invisible, so a cursor on them takes the bright Text.
	if got := cursorColor(colGone); got != colText {
		t.Errorf("cursor on a gone cell = %v, want the bright text colour", got)
	}
	if got := cursorColor(colWorking); got != colWorking {
		t.Errorf("cursor on a live cell keeps its status colour, got %v", got)
	}
	if cursorStyle(base, false).GetUnderline() {
		t.Error("an unselected cell must not be underlined")
	}
	if got := targetStyle.GetForeground(); got != colSelect {
		t.Errorf("the \u25b6 target mark must stay the select colour, got %v", got)
	}
	if got := pinStyle.GetForeground(); got == colSelect {
		t.Error("the \u2691 pin mark must not use the select colour")
	}
}

// The cursor card is told apart by its border shape (thick vs rounded) as
// well as the underline, so the cue survives any colour vision.
func TestCursorCardBorderShape(t *testing.T) {
	zone.NewGlobal()
	now := time.Now()
	m := New(&fakeBE{}, "")
	m.now = func() time.Time { return now }
	m.Update(tea.WindowSizeMsg{Width: 150, Height: 20})
	m.setAgents([]*board.Agent{{Name: "a1", Profile: "fake", Status: "waiting", StatusSince: now}})
	tw := m.grid().TileW
	m.sel = "a1"
	if sel := ansi.Strip(m.tile(m.agents[0], tw)); !strings.Contains(sel, "\u250f") {
		t.Errorf("cursor card is not the thick border:\n%s", sel)
	}
	m.sel = ""
	if plain := ansi.Strip(m.tile(m.agents[0], tw)); !strings.Contains(plain, "\u256d") {
		t.Errorf("unselected card is not the rounded border:\n%s", plain)
	}
}

// The header carries the unread-report badge (ADR-36): nothing while the
// admiral is up to date, "✉ N reports" while reports wait, and it clears
// again once they are read. Narrow panes still get no over-wide line.
func TestHeaderReportBadge(t *testing.T) {
	zone.NewGlobal()
	be := &fakeBE{}
	m := New(be, "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	head := func() string {
		m.Update(m.poll()())
		return ansi.Strip(m.header())
	}
	if h := head(); strings.Contains(h, reportMark) {
		t.Errorf("badge with nothing unread: %q", h)
	}
	be.reports = 3
	if h := head(); !strings.Contains(h, "✉ 3 reports") {
		t.Errorf("3 unread: %q", h)
	}
	be.reports = 1
	if h := head(); !strings.Contains(h, "✉ 1 report") || strings.Contains(h, "reports") {
		t.Errorf("1 unread: %q", h)
	}
	be.reports = 0
	if h := head(); strings.Contains(h, reportMark) {
		t.Errorf("badge stayed after the reports were read: %q", h)
	}
	for _, w := range []int{60, 80, 150} {
		m.Update(tea.WindowSizeMsg{Width: w, Height: 20})
		be.reports = 12
		m.Update(m.poll()())
		for _, l := range strings.Split(ansi.Strip(m.View()), "\n") {
			if ansi.StringWidth(l) > w {
				t.Errorf("w=%d: line wider than pane: %q", w, l)
			}
		}
	}
}

// TestTaskCardLineOnTile: a carded agent shows its goal, then fleet's
// done-check verdict once it has one (DESIGN §18 #5, ADR-43).
func TestTaskCardLineOnTile(t *testing.T) {
	zone.NewGlobal()
	a := &board.Agent{Name: "a1", Profile: "copilot", Status: "working", StatusSince: time.Now(),
		Card: &board.Card{Goal: "ship the card", Result: "result.md"}}
	m, _ := overview(90, 20, 1)
	m.setAgents([]*board.Agent{a})
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "◇ ship the card") {
		t.Fatalf("no card line:\n%s", v)
	}
	a.Card.Checked = &board.Check{Code: 1, Want: 0}
	m.setAgents([]*board.Agent{a})
	v = ansi.Strip(m.View())
	if !strings.Contains(v, "✗ done-check fail (exit 1, want 0)") {
		t.Fatalf("no verdict line:\n%s", v)
	}
	if strings.Contains(v, "ship the card") {
		t.Fatalf("goal still shown after the verdict:\n%s", v)
	}
	// Without a card the tile is unchanged: no card line at all.
	a.Card = nil
	m.setAgents([]*board.Agent{a})
	if v := ansi.Strip(m.View()); strings.Contains(v, "◇") || strings.Contains(v, "done-check") {
		t.Fatalf("card line without a card:\n%s", v)
	}
}

// TestTaskCardLineSharesRowWithSubagents: the fourth line holds both, and
// the tile keeps its height (the grid depends on it).
func TestTaskCardLineSharesRowWithSubagents(t *testing.T) {
	zone.NewGlobal()
	a := &board.Agent{Name: "a1", Profile: "copilot", Status: "working", StatusSince: time.Now(),
		Card:      &board.Card{Goal: "ship it", Result: "result.md"},
		Subagents: []status.Subagent{{State: status.SubRunning}, {State: status.SubRunning}}}
	m, plain := overview(90, 20, 1)
	m.setAgents([]*board.Agent{a})
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "◇ ship it") || !strings.Contains(v, subDot) {
		t.Fatalf("row 4 lost one of the two:\n%s", v)
	}
	if got, want := strings.Count(v, "\n"), strings.Count(plain, "\n"); got != want {
		t.Fatalf("tile height changed: %d lines, want %d\n%s", got, want, v)
	}
}
