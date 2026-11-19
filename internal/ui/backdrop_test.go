package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sturlabragason/fleet/internal/logo"
)

// TestBgRow: the backdrop starts with a reset and writes one escape per
// colour change, blanks without one.
func TestBgRow(t *testing.T) {
	m := New(&fakeBE{}, "")
	m.bg.esc = []string{"<0>", "<1>"}
	var b strings.Builder
	m.bgRow(&b, []logo.FlowCell{{R: 'c'}, {R: 'd', Col: 1}, {R: ' ', Col: -1}, {R: 'e', Col: 1}})
	if want := "\x1b[0m<0>c<1>d e\x1b[0m"; b.String() != want {
		t.Errorf("bgRow = %q, want %q", b.String(), want)
	}
}

// TestFrameReuse: after a backdrop tick the overview's rows are reused, not
// redrawn; any other message redraws them.
func TestFrameReuse(t *testing.T) {
	m, _ := overview(120, 30, 2)
	draws := 0
	draw := func() string { draws++; return "head\nrow" }
	m.frame(draw)
	m.Update(bgMsg{gen: -1}) // a stale tick is still only a tick
	m.frame(draw)
	if draws != 1 {
		t.Errorf("a backdrop tick redrew the overview: %d draws, want 1", draws)
	}
	m.Update(infoMsg("x"))
	if m.frame(draw); draws != 2 {
		t.Errorf("a message did not redraw the overview: %d draws, want 2", draws)
	}
}

// TestBgPatch: only changed cells right of each row's tiles are written, with
// minimal cursor moves and colour escapes, and an unchanged frame writes nothing.
func TestBgPatch(t *testing.T) {
	var buf bytes.Buffer
	m := New(&fakeBE{}, "")
	m.w, m.h = 5, 3
	m.bg.out = &SyncOut{w: &buf}
	m.bg.esc = []string{"<0>", "<1>"}
	m.bg.base = []bgLine{
		{raw: "head", pre: "head", n: 4},
		{raw: "xy", pre: "xy", n: 2},
		{raw: "", pre: "", n: 0},
	}
	// w=5, h-1=2 -> 10 cells.
	m.bg.drawn = []logo.FlowCell{
		{R: ' ', Col: -1}, {R: ' ', Col: -1}, {R: ':', Col: 0}, {R: '+', Col: 0}, {R: ' ', Col: -1},
		{R: ' ', Col: -1}, {R: ' ', Col: -1}, {R: ' ', Col: -1}, {R: ' ', Col: -1}, {R: ' ', Col: -1},
	}
	next := append([]logo.FlowCell(nil), m.bg.drawn...)
	next[0] = logo.FlowCell{R: '@', Col: 1} // under tile "xy" on row 1: must be skipped
	next[2] = logo.FlowCell{R: '+', Col: 0} // row 1 (1-based 2), col 2 (1-based 3)
	next[3] = logo.FlowCell{R: '@', Col: 1} // row 1 (1-based 2), col 3 (1-based 4)
	next[4] = logo.FlowCell{R: ':', Col: 0} // row 1 (1-based 2), col 4 (1-based 5): same colour <0>, 1-cell jump right
	next[6] = logo.FlowCell{R: ':', Col: 1} // row 2 (1-based 3), col 1 (1-based 2): same colour <1>
	m.bgPatch(next)
	want := "\x1b[?2026h\x1b[0m<0>\x1b[2;3H+\x1b[C:<1>\x1b[2;4H@\x1b[3;2H:\x1b[0m\x1b[3;1H\x1b[?2026l"
	if got := buf.String(); got != want {
		t.Errorf("bgPatch = %q, want %q", got, want)
	}
	// Running again with the same cells writes nothing.
	buf.Reset()
	m.bgPatch(next)
	if buf.Len() != 0 {
		t.Errorf("unchanged bgPatch wrote %q, want empty", buf.String())
	}
}

// TestFramePatchSync: frame waits for Bubble Tea to flush the full frame before
// switching to cell-delta patches, returns lastView unchanged while patching,
// and resets on WindowSizeMsg.
func TestFramePatchSync(t *testing.T) {
	var buf bytes.Buffer
	m := New(&fakeBE{}, "")
	m.w, m.h = 20, 6
	m.bg.out = &SyncOut{w: &buf}
	// Force colour escapes so the backdrop runs in a colourless test terminal.
	esc := make([]string, 2*logo.FlowLevels)
	for i := range esc {
		esc[i] = "<c>"
	}
	draw := func() string { return "head\n\n\n\n\n" }
	_ = m.frame(draw) // initializes m.bg.key and m.bg.flow
	m.bg.esc = esc
	m.bg.frameT = -1
	v0 := m.frame(draw)
	// Before Bubble Tea flushes v0, a tick must NOT patch directly.
	m.bg.t = 1
	m.Update(bgMsg{gen: -1})
	v1 := m.frame(draw)
	if buf.Len() != 0 {
		t.Fatalf("patched before full frame flushed: %q", buf.String())
	}
	// Simulate Bubble Tea flushing v1 to SyncOut.
	_, _ = m.bg.out.Write([]byte(v1))
	buf.Reset()
	// Now a backdrop tick patches directly and returns v1 unchanged.
	m.bg.t = 2
	m.Update(bgMsg{gen: -1})
	if v2 := m.frame(draw); v2 != v1 {
		t.Errorf("patching tick changed View() string (would trigger full Bubble Tea redraw)")
	}
	if buf.Len() == 0 {
		t.Errorf("patching tick wrote no cell delta after flush")
	}
	if len(v0) == 0 {
		t.Errorf("initial frame was empty")
	}
	// A non-tick message whose draw() output is unchanged keeps patching without a full repaint.
	buf.Reset()
	m.Update(animMsg{})
	if v3 := m.frame(draw); v3 != v1 {
		t.Errorf("unchanged non-tick message triggered a full redraw")
	}
	if buf.Len() != 0 {
		t.Errorf("unchanged non-tick message wrote %q, want empty", buf.String())
	}
	// WindowSizeMsg clears lastView so the next frame waits for Bubble Tea again.
	buf.Reset()
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 6})
	m.bg.t = 3
	_ = m.frame(draw)
	if buf.Len() != 0 {
		t.Errorf("patched directly right after WindowSizeMsg: %q", buf.String())
	}
}

// TestBackdropTicks: the backdrop ticks only while it runs; still, off or
// paused it starts no timer; z pauses it across a restart; a stale tick is
// dropped.
func TestBackdropTicks(t *testing.T) {
	dir := t.TempDir()
	m := New(&fakeBE{}, dir)
	if m.bgStart() == nil {
		t.Fatal("a running backdrop starts no tick")
	}
	m.SetStill(true)
	if m.bgStart() != nil {
		t.Error("a still backdrop ticks")
	}
	m.SetStill(false)
	m.SetBackdrop(false)
	if m.bgStart() != nil {
		t.Error("a backdrop that is off ticks")
	}
	m.SetBackdrop(true)
	m.bgStart()
	stale := bgMsg{m.bg.gen - 1}
	if m.bgTick(stale) != nil {
		t.Error("a stale tick chain keeps running")
	}
	if m.key("z"); !m.bg.paused || m.bgStart() != nil {
		t.Error("z does not pause the backdrop")
	}
	if !New(&fakeBE{}, dir).bg.paused {
		t.Error("the pause is not kept across a restart")
	}
	if cmd := m.key("z"); m.bg.paused || cmd == nil {
		t.Error("z again does not start the backdrop")
	}
}

// TestBackdropLean: the strands ease toward the selected tile's row and
// never jump there in one tick.
func TestBackdropLean(t *testing.T) {
	m, _ := overview(160, 40, 6)
	now := m.now()
	m.now = func() time.Time { return now }
	m.bgStart()
	want, ok := m.selRow()
	if !ok {
		t.Fatal("the selected tile has no row")
	}
	prev := 0.0
	for range 80 {
		now = now.Add(250 * time.Millisecond)
		m.bgTick(bgMsg{m.bg.gen})
		if step := m.bg.lean.Pull - prev; step <= 0 || step > 0.1 {
			t.Fatalf("pull stepped %.3f, want a small rise", step)
		}
		prev = m.bg.lean.Pull
	}
	if m.bg.lean.Pull < 0.85 || m.bg.lean.Row < want*0.85 {
		t.Errorf("after 20 s lean = %+v, want close to row %.1f at full pull", m.bg.lean, want)
	}
}

// TestBackdropShown: a terminal without colour gets no backdrop, and the
// help is never drawn over it.
func TestBackdropShown(t *testing.T) {
	m, _ := overview(120, 30, 2)
	m.bg.key, m.bg.esc = "", nil
	lines := strings.Split(m.View(), "\n")
	if m.bg.esc == nil { // a test terminal has no colour: nothing is drawn
		for _, l := range lines {
			if strings.ContainsAny(ansi.Strip(l), "@%#") {
				t.Fatalf("backdrop drawn without colour: %q", ansi.Strip(l))
			}
		}
	}
	m.help = true
	if _, ok := m.bgShown(); ok {
		t.Error("backdrop shown behind the help")
	}
}
