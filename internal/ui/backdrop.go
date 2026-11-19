package ui

import (
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sturlabragason/fleet/internal/logo"
	"github.com/sturlabragason/fleet/internal/theme"
)

// The overview's backdrop is the fleet mark stretched over the whole pane
// below the header, behind the tiles (ADR-176, ADR-180): its strands drift
// organically, its lanes hold still and breathe on their own rhythms, and it
// leans toward the selected tile.
const (
	bgFPS    = 12  // frames a second while it runs; cell-delta patching keeps tmux CPU low (ADR-180)
	bgDim    = 0.4 // behind tiles: the tiles stay the brightest thing
	leanEase = 8.0 // seconds to cover about 63% of a lean change: slow enough to stay under TestFlowSmooth's quarter row a frame
)

// SyncOut wraps a terminal output file so Bubble Tea's full frames and our
// cell-delta backdrop patches never interleave, while exposing Fd/Read/Close
// for Bubble Tea's tty detection. Holding *os.File as a private field (instead
// of embedding it) ensures io.WriteString cannot bypass mu.
type SyncOut struct {
	f   *os.File
	mu  sync.Mutex
	w   io.Writer
	seq atomic.Uint64
}

// NewSyncOut wraps f for use with both tea.WithOutput and Model.SetBackdropOut.
func NewSyncOut(f *os.File) *SyncOut { return &SyncOut{f: f, w: f} }

// Fd returns the wrapped file's descriptor for Bubble Tea's tty check.
func (s *SyncOut) Fd() uintptr { return s.f.Fd() }

// Read delegates to the wrapped file to satisfy term.File.
func (s *SyncOut) Read(p []byte) (int, error) { return s.f.Read(p) }

// Close closes the wrapped file.
func (s *SyncOut) Close() error { return s.f.Close() }

// Write writes a Bubble Tea frame and records that a full flush happened.
func (s *SyncOut) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.w.Write(p)
	s.seq.Add(1)
	return n, err
}

// WriteString serializes Bubble Tea's control writes (standardRenderer.execute)
// under mu without bumping seq, since they are not full frame flushes.
func (s *SyncOut) WriteString(str string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return io.WriteString(s.w, str)
}

func (s *SyncOut) patch(p []byte) {
	if len(p) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.w.Write(p)
}

type bgMsg struct{ gen int }

type backdrop struct {
	off    bool // NO_COLOR or TERM=dumb: no backdrop at all
	paused bool // z, kept in ui.json
	gen    int  // the running tick chain; a stale bgMsg is dropped
	t      float64
	last   time.Time
	lean   logo.Lean
	flow   *logo.Flow
	cells  []logo.FlowCell // newly rendered cells
	drawn  []logo.FlowCell // cells currently on screen
	key    string          // size, theme and dim the cells and escapes were drawn for
	esc    []string        // colour escapes; nil: the terminal has no colour
	frameT float64         // t the cells were drawn at
	// base is the overview without the backdrop, one entry per pane row.
	// A frame whose only news is a bgMsg reuses it, so backdrop ticks never
	// redraw the tiles (tickOnly).
	base     []bgLine
	baseRaw  string
	tickOnly bool
	out      *SyncOut // nil in tests: falls back to full View() strings
	lastView string   // last full frame handed to bubbletea
	fullSeq  uint64   // out.seq when lastView was handed to bubbletea
	patchBuf []byte   // scratch buffer for cell-delta escape output
	buckets  [2*logo.FlowLevels + 1][]bgCell
}

type bgCell struct {
	r, c int16
	ch   rune
}

// bgLine is one row of the overview: as drawn, and cut after its last
// drawn cell (n cells), where the backdrop starts.
type bgLine struct {
	raw, pre string
	n        int
}

// SetBackdrop turns the backdrop off for NO_COLOR and TERM=dumb, where its
// density glyphs would only fight the tiles.
func (m *Model) SetBackdrop(on bool) { m.bg.off = !on }

// SetBackdropOut lets backdrop ticks patch only changed cells directly to the
// terminal instead of handing Bubble Tea a full-pane string every frame.
func (m *Model) SetBackdropOut(out *SyncOut) { m.bg.out = out }

// SetBackdropStart sets the starting clock of the drift, so an animated launch
// begins at a fresh point in the motion while a still frame stays at t=0.
func (m *Model) SetBackdropStart(t float64) { m.bg.t = t }

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
	gen := m.bg.gen
	return tea.Tick(time.Second/bgFPS, func(time.Time) tea.Msg { return bgMsg{gen} })
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
	g := m.grid()
	if _, tiered := m.tiers(); tiered || m.help || g.Cols == 0 {
		return 0, false
	}
	for j, a := range m.agents[:min(g.Cards, len(m.agents))] {
		if a.Name == m.sel {
			return float64(j/g.Cols*m.cfg.TileH) + float64(m.cfg.TileH)/2, true
		}
	}
	return 0, false
}

// frame is the pane: the overview's rows (redrawn unless only the backdrop
// ticked) with the backdrop in every cell right of what each row shows,
// below the header. When the last full frame has already been flushed to
// m.bg.out and only the backdrop changed, it patches only the changed cells
// directly and returns lastView unchanged so Bubble Tea skips a full repaint.
func (m *Model) frame(draw func() string) string {
	baseChanged := false
	if !m.bg.tickOnly || len(m.bg.base) == 0 {
		raw := draw()
		if raw != m.bg.baseRaw || len(m.bg.base) == 0 {
			m.bg.baseRaw = raw
			m.bg.base = m.bg.base[:0]
			for l := range strings.SplitSeq(raw, "\n") {
				n := ansi.StringWidth(strings.TrimRight(ansi.Strip(l), " "))
				m.bg.base = append(m.bg.base, bgLine{raw: l, pre: ansi.Truncate(l, n, ""), n: n})
			}
			baseChanged = true
		}
	}
	cells, keyChanged := m.bgCells()
	if !baseChanged && !keyChanged && m.bg.out != nil && m.bg.lastView != "" && m.bg.out.seq.Load() > m.bg.fullSeq {
		if cells != nil {
			m.bgPatch(cells)
		}
		return m.bg.lastView
	}
	var b strings.Builder
	for r, l := range m.bg.base {
		if r > 0 {
			b.WriteByte('\n')
		}
		if r == 0 || cells == nil || l.n >= m.w || r > m.h-1 {
			b.WriteString(l.raw)
			continue
		}
		b.WriteString(l.pre)
		m.bgRow(&b, cells[(r-1)*m.w+l.n:r*m.w])
	}
	s := b.String()
	if cells != nil {
		copy(m.bg.drawn, cells)
	}
	if m.bg.out != nil && s != m.bg.lastView {
		m.bg.fullSeq = m.bg.out.seq.Load()
	}
	m.bg.lastView = s
	return s
}

// bgPatch writes only backdrop cells that changed since m.bg.drawn straight to
// the terminal, grouped by colour index so each SGR escape is emitted at most
// once per frame, skipping tile columns and the header row (ADR-180).
func (m *Model) bgPatch(cells []logo.FlowCell) {
	for i := range m.bg.buckets {
		m.bg.buckets[i] = m.bg.buckets[i][:0]
	}
	rows := min(len(m.bg.base), m.h)
	changed := false
	for r := 1; r < rows; r++ {
		start := m.bg.base[r].n
		if start >= m.w {
			continue
		}
		rowOff := (r - 1) * m.w
		for c := start; c < m.w; c++ {
			idx := rowOff + c
			cell := cells[idx]
			if cell == m.bg.drawn[idx] {
				continue
			}
			m.bg.drawn[idx] = cell
			b := int(cell.Col) + 1
			m.bg.buckets[b] = append(m.bg.buckets[b], bgCell{int16(r), int16(c), cell.R})
			changed = true
		}
	}
	if !changed {
		return
	}
	buf := append(m.bg.patchBuf[:0], "\x1b[?2026h\x1b[0m"...)
	curR, curC := -1, -1
	for b, list := range m.bg.buckets {
		if len(list) == 0 {
			continue
		}
		if b > 0 {
			buf = append(buf, m.bg.esc[b-1]...)
		}
		for _, p := range list {
			r, c := int(p.r), int(p.c)
			if r != curR || c != curC {
				buf = appendMove(buf, curR, curC, r, c)
			}
			buf = utf8.AppendRune(buf, p.ch)
			curR, curC = r, c+1
		}
	}
	buf = append(buf, "\x1b[0m\x1b["...)
	buf = strconv.AppendInt(buf, int64(m.h), 10)
	buf = append(buf, ";1H\x1b[?2026l"...)
	m.bg.patchBuf = buf
	m.bg.out.patch(buf)
}

func appendMove(buf []byte, curR, curC, r, c int) []byte {
	if r == curR && c > curC {
		d := c - curC
		buf = append(buf, "\x1b["...)
		if d > 1 {
			buf = strconv.AppendInt(buf, int64(d), 10)
		}
		return append(buf, 'C')
	}
	buf = append(buf, "\x1b["...)
	buf = strconv.AppendInt(buf, int64(r+1), 10)
	buf = append(buf, ';')
	buf = strconv.AppendInt(buf, int64(c+1), 10)
	return append(buf, 'H')
}

// bgCells is this frame's backdrop, w×(h-1) cells, nil when none is shown,
// and whether the backdrop's geometry, theme, brightness or visibility changed.
func (m *Model) bgCells() ([]logo.FlowCell, bool) {
	dim, ok := m.bgShown()
	if !ok || m.h < 2 || m.w < 1 {
		changed := m.bg.key != ""
		m.bg.key = ""
		return nil, changed
	}
	w, h := m.w, m.h-1
	keyChanged := false
	if key := fmt.Sprint(theme.Base, theme.Text, theme.Select, dim, w, h); key != m.bg.key {
		m.bg.key, m.bg.esc = key, logo.FlowEscapes(dim)
		m.bg.flow = logo.NewFlow(w, h)
		m.bg.cells = make([]logo.FlowCell, w*h)
		m.bg.drawn = make([]logo.FlowCell, w*h)
		m.bg.frameT = -1
		keyChanged = true
	}
	if m.bg.esc == nil {
		return nil, keyChanged
	}
	if m.bg.frameT != m.bg.t {
		m.bg.flow.Frame(m.bg.t, m.bg.lean, m.bg.cells)
		m.bg.frameT = m.bg.t
	}
	return m.bg.cells, keyChanged
}

// bgRow writes cells after a reset, one escape per colour change.
func (m *Model) bgRow(b *strings.Builder, cells []logo.FlowCell) {
	b.WriteString("\x1b[0m")
	cur := int8(-1)
	for _, c := range cells {
		if c.Col >= 0 && c.Col != cur {
			b.WriteString(m.bg.esc[c.Col])
			cur = c.Col
		}
		b.WriteRune(c.R)
	}
	b.WriteString("\x1b[0m")
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
