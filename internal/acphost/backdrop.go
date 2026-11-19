package acphost

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/colorprofile"

	"github.com/sturlabragason/fleet/internal/logo"
)

// acpBgFPS is the calm tick rate of a focused ACP pane's blurred frosted-glass
// backdrop; unfocused ACP panes run no timer at all so background agents cost
// zero backdrop CPU (ADR-183).
const acpBgFPS = 8

type acpBackdrop struct {
	enabled   bool
	still     bool
	seed      uint64
	t         float64
	frameT    float64
	last      time.Time
	flow      *logo.Flow
	w, h      int
	bodyStart int
	bodyEnd   int
	cells     []logo.FlowCell
	drawn     []logo.FlowCell
	esc       []string
	lines     []logo.BackdropLine
	patcher   logo.BackdropPatcher
	timer     *time.Timer
	gen       int
}

// enableBackdrop turns on the blurred low-opacity frosted-glass backdrop for
// live ACP panes, respecting animation = false / FLEET_NO_ANIM (still).
func (v *view) enableBackdrop(still bool, seed uint64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.bg.enabled = true
	v.bg.still = still
	v.bg.seed = seed
	v.bg.frameT = -1
	if !still {
		v.bg.t = 12 // warm start so the strands are already roaming
	}
	v.syncBgTimer()
}

// overlay composites blurred frosted-glass backdrop cells into the transparent
// blank spans of rows[bodyStart:bodyEnd]. Caller holds v.mu.
func (b *acpBackdrop) overlay(rows []string, w, bodyStart, bodyEnd int, p colorprofile.Profile) {
	if !b.enabled || w < 1 || bodyEnd <= bodyStart || bodyEnd > len(rows) {
		return
	}
	if b.esc == nil {
		b.esc = logo.GlassEscapesFor(p)
		if b.esc == nil {
			return
		}
	}
	bodyH := bodyEnd - bodyStart
	if b.flow == nil || b.w != w || b.h != bodyH {
		if b.seed != 0 {
			b.flow = logo.NewFlowSeeded(w, bodyH, b.seed)
		} else {
			b.flow = logo.NewFlow(w, bodyH)
		}
		b.w, b.h = w, bodyH
		b.cells = make([]logo.FlowCell, w*bodyH)
		b.drawn = make([]logo.FlowCell, w*bodyH)
		b.frameT = -1
	}
	if b.frameT != b.t {
		b.flow.Frame(b.t, logo.Lean{}, b.cells)
		b.frameT = b.t
	}
	b.bodyStart, b.bodyEnd = bodyStart, bodyEnd
	if cap(b.lines) < len(rows) {
		b.lines = make([]logo.BackdropLine, len(rows))
	} else {
		b.lines = b.lines[:len(rows)]
	}
	var sb strings.Builder
	for r := bodyStart; r < bodyEnd; r++ {
		line := logo.ParseBackdropLine(rows[r], w, true)
		b.lines[r] = line
		if len(line.Spans) == 0 {
			continue
		}
		sb.Reset()
		rowOff := (r - bodyStart) * w
		for si, sp := range line.Spans {
			sb.WriteString(line.Segs[si])
			logo.WriteBackdropSpan(&sb, b.cells[rowOff+sp.C0:rowOff+sp.C1], b.drawn[rowOff+sp.C0:rowOff+sp.C1], b.esc, true, sp.Restore)
		}
		sb.WriteString(line.Segs[len(line.Spans)])
		rows[r] = sb.String()
	}
}

// syncBgTimer runs the 8 fps cell-delta ticker only while this ACP pane is
// focused, animated, open and has colour. Caller holds v.mu.
func (v *view) syncBgTimer() {
	if !v.bg.enabled || v.bg.still || v.closed || v.focus != 1 || !v.pt.color() {
		v.bg.stop()
		return
	}
	if v.bg.timer != nil {
		return
	}
	v.bg.gen++
	gen := v.bg.gen
	v.bg.last = time.Now()
	v.bg.timer = time.AfterFunc(time.Second/acpBgFPS, func() { v.bgTick(gen) })
}

func (b *acpBackdrop) stop() {
	b.gen++
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
}

func (v *view) bgTick(gen int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || gen != v.bg.gen || v.focus != 1 || !v.bg.enabled || v.bg.still {
		v.bg.timer = nil
		return
	}
	now := time.Now()
	dt := min(max(now.Sub(v.bg.last).Seconds(), 0), 0.5)
	v.bg.t += dt
	v.bg.last = now
	if v.bg.flow != nil && v.bg.esc != nil && v.shown != nil && v.bg.bodyEnd <= len(v.bg.lines) {
		v.bg.flow.Frame(v.bg.t, logo.Lean{}, v.bg.cells)
		v.bg.frameT = v.bg.t
		if inner := v.bg.patcher.Patch(v.bg.lines, v.bg.bodyStart, v.bg.bodyEnd, v.bg.w, v.bg.bodyStart, v.bg.cells, v.bg.drawn, v.bg.esc); len(inner) > 0 {
			_, _ = fmt.Fprintf(v.out, "\x1b[?2026h\x1b[?25l%s\x1b[%d;%dH\x1b[?25h\x1b[?2026l", inner, v.cursor[1], v.cursor[0])
		}
	}
	v.bg.timer = time.AfterFunc(time.Second/acpBgFPS, func() { v.bgTick(gen) })
}
