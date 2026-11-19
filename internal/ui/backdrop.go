package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sturlabragason/fleet/internal/logo"
	"github.com/sturlabragason/fleet/internal/theme"
)

// The overview's backdrop is the fleet mark stretched over the whole pane
// below the header, behind the tiles (ADR-176): its strands flow, its lanes
// hold still and breathe, and it leans toward the selected tile.
const (
	bgFPS      = 4                // frames a second while it runs
	bgFocusFPS = 8                // for bgBoost after the pane gains focus
	bgBoost    = 10 * time.Second //
	bgDim      = 0.4              // behind tiles: the tiles stay the brightest thing
	leanEase   = 8.0              // seconds to cover about 63% of a lean change: slow enough to stay under TestFlowSmooth's half row a frame
)

type bgMsg struct{ gen int }

type backdrop struct {
	off    bool // NO_COLOR or TERM=dumb: no backdrop at all
	paused bool // z, kept in ui.json
	gen    int  // the running tick chain; a stale bgMsg is dropped
	t      float64
	last   time.Time
	boost  time.Time // faster frames until then
	lean   logo.Lean
	flow   *logo.Flow
	cells  []logo.FlowCell
	key    string   // size, theme and dim the cells and escapes were drawn for
	esc    []string // colour escapes; nil: the terminal has no colour
	frameT float64  // t the cells were drawn at
}

// SetBackdrop turns the backdrop off for NO_COLOR and TERM=dumb, where its
// density glyphs would only fight the tiles.
func (m *Model) SetBackdrop(on bool) { m.bg.off = !on }

// bgRuns reports whether the backdrop ticks: on, moving and not paused.
func (m *Model) bgRuns() bool { return !m.bg.off && !m.still && !m.bg.paused }

// bgStart starts a new tick chain; the old one dies on its next tick.
func (m *Model) bgStart() tea.Cmd {
	m.bg.gen++
	m.bg.last = m.now()
	return m.bgNext()
}

func (m *Model) bgNext() tea.Cmd {
	if !m.bgRuns() {
		return nil // no timer at all: a still backdrop costs nothing
	}
	fps := bgFPS
	if m.now().Before(m.bg.boost) {
		fps = bgFocusFPS
	}
	gen := m.bg.gen
	return tea.Tick(time.Second/time.Duration(fps), func(time.Time) tea.Msg { return bgMsg{gen} })
}

// bgTick advances the backdrop's clock and eases its lean toward the
// selected tile.
func (m *Model) bgTick(msg bgMsg) tea.Cmd {
	if msg.gen != m.bg.gen || !m.bgRuns() {
		return nil
	}
	now := m.now()
	dt := min(max(now.Sub(m.bg.last).Seconds(), 0), 0.5)
	m.bg.t += dt
	m.bg.last = now
	k := 1 - math.Exp(-dt/leanEase)
	target := logo.Lean{Row: m.bg.lean.Row}
	if r, ok := m.selRow(); ok {
		target = logo.Lean{Row: r, Pull: 1}
	}
	m.bg.lean.Row += (target.Row - m.bg.lean.Row) * k
	m.bg.lean.Pull += (target.Pull - m.bg.lean.Pull) * k
	return m.bgNext()
}

// bgFocus speeds the backdrop up for a while after the pane gains focus.
func (m *Model) bgFocus() tea.Cmd {
	m.bg.boost = m.now().Add(bgBoost)
	return m.bgStart()
}

// togglePause is z: hold the backdrop still, or let it move again; the
// choice survives a restart.
func (m *Model) togglePause() tea.Cmd {
	m.bg.paused = !m.bg.paused
	m.saveCursor()
	if m.bg.paused {
		m.msg, m.msgAt = "backdrop paused · z moves it again", m.now()
		return nil
	}
	return m.bgStart()
}

// selRow is the middle row of the selected tile in the backdrop's grid
// (the pane below the header), false when the grid doesn't show it.
func (m *Model) selRow() (float64, bool) {
	if _, tiered := m.tiers(); tiered || m.help {
		return 0, false
	}
	g := m.grid()
	for j, a := range m.agents[:min(g.Cards, len(m.agents))] {
		if a.Name == m.sel && g.Cols > 0 {
			return float64(j/g.Cols*m.cfg.TileH) + float64(m.cfg.TileH)/2, true
		}
	}
	return 0, false
}

// backdropOver draws the backdrop into every cell to the right of what the
// lines already show, below the header; lines are the pane's rows after
// zone.Scan, so cutting a row's trailing blanks can't cut a zone marker.
func (m *Model) backdropOver(lines []string) []string {
	dim, ok := m.bgShown()
	if !ok || m.h < 2 || m.w < 1 {
		return lines
	}
	w, h := m.w, m.h-1
	key := fmt.Sprint(theme.Base, theme.Text, theme.Select, dim, w, h)
	if key != m.bg.key {
		m.bg.key, m.bg.esc = key, logo.FlowEscapes(dim)
		m.bg.flow, m.bg.cells = logo.NewFlow(w, h), make([]logo.FlowCell, w*h)
		m.bg.frameT = -1
	}
	if m.bg.esc == nil {
		return lines
	}
	if m.bg.frameT != m.bg.t {
		m.bg.flow.Frame(m.bg.t, m.bg.lean, m.bg.cells)
		m.bg.frameT = m.bg.t
	}
	for len(lines) < m.h {
		lines = append(lines, "")
	}
	for r := 1; r < m.h; r++ {
		lines[r] = m.bgRow(lines[r], m.bg.cells[(r-1)*w:r*w])
	}
	return lines
}

// bgRow keeps what line shows up to its last drawn cell and fills the rest
// of the row from cells.
func (m *Model) bgRow(line string, cells []logo.FlowCell) string {
	n := ansi.StringWidth(strings.TrimRight(ansi.Strip(line), " "))
	if n >= len(cells) {
		return line
	}
	var b strings.Builder
	b.WriteString(ansi.Truncate(line, n, ""))
	b.WriteString("\x1b[0m")
	cur := int8(-1)
	for _, c := range cells[n:] {
		if c.Col >= 0 && c.Col != cur {
			b.WriteString(m.bg.esc[c.Col])
			cur = c.Col
		}
		b.WriteRune(c.R)
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// bgShown reports whether the backdrop belongs on screen now, and how
// bright: on the tile overview (the theme picker above it included), at
// bgDim behind tiles and full on an empty overview; never behind the help,
// the w list, an answer or an extension's view, which are text to read.
func (m *Model) bgShown() (dim float64, ok bool) {
	if m.bg.off || m.ans != nil || m.wv != nil || m.help || extView(m) != nil {
		return 0, false
	}
	if len(m.agents) == 0 {
		return 1, true
	}
	return bgDim, true
}

// bgOnEmpty reports whether an empty overview shows the backdrop instead of
// the static splash: the backdrop is on and the terminal draws colour.
func (m *Model) bgOnEmpty() bool {
	return !m.bg.off && logo.FlowEscapes(1) != nil
}
