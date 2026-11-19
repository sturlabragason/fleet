package logo

import (
	"image/color"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// Blurred frosted-glass backdrop parameters shared by the overview and ACP
// panes (ADR-181, ADR-183).
const (
	GlassDim     = 0.22           // low opacity inside tiles, panels and ACP panes
	CrispCols    = 2 * FlowLevels // crisp colour slots (ink + accent)
	GlassSteps   = FlowLevels / 2 // 8 shades per role: soft blur + half the cell churn
	BackdropCols = CrispCols + 2*GlassSteps
)

// BackdropSpan is a half-open column range [C0, C1) on a row that shows the
// backdrop instead of foreground content. Interior spans inside tiles and
// panels use blurred low-opacity glass; open space right of tiles uses crisp cells.
type BackdropSpan struct {
	C0, C1  int
	Glass   bool
	Restore string // active SGR sequence to replay after this span, if any
}

// BackdropLine is one parsed screen row: verbatim foreground segments
// interleaved with backdrop spans (len(Segs) == len(Spans)+1 when Spans exist).
type BackdropLine struct {
	Raw   string
	Segs  []string
	Spans []BackdropSpan
}

// GlassCell maps a raw FlowCell to its blurred, low-opacity frosted-glass
// counterpart (soft stipple ░ / ·, quantized to 8 shades per role) for empty
// cells inside tiles, panels and ACP panes (ADR-183).
func GlassCell(c FlowCell) FlowCell {
	if c.Col < 0 {
		return c
	}
	role := c.Col / FlowLevels
	step := (c.Col % FlowLevels) / 2
	ch := '░'
	if c.R == ':' {
		ch = '·'
	}
	return FlowCell{R: ch, Col: CrispCols + role*GlassSteps + step}
}

// BackdropEscapes builds the combined colour escape table for the terminal's
// lipgloss colour profile: CrispCols open-area colours at dim followed by
// 2*GlassSteps blurred glass colours at GlassDim. Returns nil without colour.
func BackdropEscapes(dim float64) []string {
	crisp := FlowEscapes(dim)
	if crisp == nil {
		return nil
	}
	rawGlass := FlowEscapes(GlassDim)
	if rawGlass == nil {
		return nil
	}
	out := make([]string, 0, BackdropCols)
	out = append(out, crisp...)
	for role := range 2 {
		base := role * FlowLevels
		for step := range GlassSteps {
			out = append(out, rawGlass[base+step*2+1])
		}
	}
	return out
}

// GlassEscapesFor builds a BackdropCols escape table with blurred glass
// colours encoded for an explicit colorprofile.Profile (used by acphost).
// Returns nil when p has no colour.
func GlassEscapesFor(p colorprofile.Profile) []string {
	if p <= colorprofile.Ascii {
		return nil
	}
	rawGlass := FlowColours(GlassDim)
	out := make([]string, BackdropCols)
	for role := range 2 {
		base := role * FlowLevels
		for step := range GlassSteps {
			c, ok := parseHexColor(rawGlass[base+step*2+1])
			if !ok {
				return nil
			}
			var st ansi.Style
			st = st.ForegroundColor(p.Convert(c))
			out[CrispCols+role*GlassSteps+step] = st.String()
		}
	}
	return out
}

func parseHexColor(s string) (color.RGBA, bool) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return color.RGBA{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.RGBA{}, false
	}
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}, true
}

// WriteBackdropSpan writes one backdrop span (crisp or blurred glass), records
// the rendered cells into drawn when non-nil so BackdropPatcher diffs
// apples-to-apples, and restores any active foreground SGR after the span.
func WriteBackdropSpan(b *strings.Builder, cells, drawn []FlowCell, esc []string, glass bool, restore string) {
	b.WriteString("\x1b[0m")
	cur := int8(-1)
	for i, c := range cells {
		if glass {
			c = GlassCell(c)
		}
		if i < len(drawn) {
			drawn[i] = c
		}
		if c.Col >= 0 && c.Col != cur {
			b.WriteString(esc[c.Col])
			cur = c.Col
		}
		b.WriteRune(c.R)
	}
	b.WriteString("\x1b[0m")
	if restore != "" {
		b.WriteString(restore)
	}
}

type patchCell struct {
	r, c int16
	ch   rune
}

// BackdropPatcher emits colour-bucketed cell-delta ANSI sequences for changed
// backdrop cells, reusing its scratch buffers across frames with zero allocs.
type BackdropPatcher struct {
	buf     []byte
	buckets [BackdropCols + 1][]patchCell
}

// Patch diffs cells against drawn across lines[rowStart:rowEnd], updates drawn
// in place, and returns the ANSI bytes (starting and ending with \x1b[0m) for
// only the changed cells, or nil when nothing changed. rowOffset is subtracted
// from each screen row index r to index into cells and drawn.
func (p *BackdropPatcher) Patch(lines []BackdropLine, rowStart, rowEnd, w, rowOffset int, cells, drawn []FlowCell, esc []string) []byte {
	for i := range p.buckets {
		p.buckets[i] = p.buckets[i][:0]
	}
	changed := false
	for r := rowStart; r < rowEnd; r++ {
		rowOff := (r - rowOffset) * w
		for _, sp := range lines[r].Spans {
			for c := sp.C0; c < sp.C1; c++ {
				idx := rowOff + c
				cell := cells[idx]
				if sp.Glass {
					cell = GlassCell(cell)
				}
				if cell == drawn[idx] {
					continue
				}
				drawn[idx] = cell
				b := int(cell.Col) + 1
				p.buckets[b] = append(p.buckets[b], patchCell{int16(r), int16(c), cell.R})
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	buf := append(p.buf[:0], "\x1b[0m"...)
	curR, curC := -1, -1
	for b, list := range p.buckets {
		if len(list) == 0 {
			continue
		}
		if b > 0 {
			buf = append(buf, esc[b-1]...)
		}
		for _, cell := range list {
			r, c := int(cell.r), int(cell.c)
			if r != curR || c != curC {
				buf = appendCursorMove(buf, curR, curC, r, c)
			}
			buf = utf8.AppendRune(buf, cell.ch)
			curR, curC = r, c+1
		}
	}
	buf = append(buf, "\x1b[0m"...)
	p.buf = buf
	return buf
}

func appendCursorMove(buf []byte, curR, curC, r, c int) []byte {
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

type bgLineScanner struct {
	line       string
	width      int
	fullGlass  bool
	out        BackdropLine
	col        int
	opaqueMask uint8
	sgr        strings.Builder
	segStart   int
	inBlank    bool
	blankCol0  int
	blankPos0  int
	blankPos1  int
	blankSGR   string
}

// ParseBackdropLine splits one styled terminal row into verbatim foreground
// segments and transparent blank spans suitable for backdrop compositing
// (ADR-183).
//
// Plain spaces with no active background, reverse, underline or strikethrough
// SGR are transparent:
//   - Trailing space after the last visible cell connects to width (crisp when
//     !fullGlass, blurred glass with a 1-cell breathing margin when fullGlass).
//   - Interior runs of 3+ plain spaces keep a 1-cell breathing margin on each
//     side so words and borders never touch backdrop glyphs; leading indents
//     of 2+ spaces keep a 1-cell margin before the text.
func ParseBackdropLine(line string, width int, fullGlass bool) BackdropLine {
	s := bgLineScanner{line: line, width: width, fullGlass: fullGlass, out: BackdropLine{Raw: line}}
	if width <= 0 {
		return s.out
	}
	for i := 0; i < len(line); {
		switch {
		case line[i] == '\x1b':
			i = s.consumeEscape(i)
		case line[i] == ' ' && s.opaqueMask == 0:
			s.consumeBlank(i)
			i++
		default:
			i = s.consumeText(i)
		}
	}
	s.finish()
	return s.out
}

func (s *bgLineScanner) consumeEscape(i int) int {
	end, isSGR, params := scanEscape(s.line, i)
	nextMask := s.opaqueMask
	if isSGR {
		nextMask = applySGRMask(s.opaqueMask, params)
	}
	if s.inBlank && nextMask != 0 {
		s.flushBlank(false)
	}
	if isSGR {
		if params == "" || params == "0" {
			s.sgr.Reset()
		} else {
			s.sgr.WriteString(s.line[i:end])
		}
		if s.inBlank {
			s.blankSGR = s.sgr.String()
		}
	}
	s.opaqueMask = nextMask
	return end
}

func (s *bgLineScanner) consumeBlank(i int) {
	if !s.inBlank {
		s.inBlank = true
		s.blankCol0 = s.col
		s.blankPos0 = i
		s.blankSGR = s.sgr.String()
	}
	s.blankPos1 = i
	s.col++
}

func (s *bgLineScanner) consumeText(i int) int {
	if s.inBlank {
		s.flushBlank(false)
	}
	j := i
	ascii := true
	for j < len(s.line) && s.line[j] != '\x1b' && (s.line[j] != ' ' || s.opaqueMask != 0) {
		if s.line[j] >= 0x80 || s.line[j] < 0x20 {
			ascii = false
		}
		j++
	}
	if ascii {
		s.col += j - i
	} else {
		s.col += ansi.StringWidth(s.line[i:j])
	}
	return j
}

func (s *bgLineScanner) flushBlank(trailing bool) {
	if !s.inBlank {
		return
	}
	s.inBlank = false
	if trailing {
		c0, cut := s.blankCol0, s.blankPos0
		if s.fullGlass && s.blankCol0 > 0 {
			c0 = s.blankCol0 + 1
			cut = advanceOneSpace(s.line, s.blankPos0)
		}
		if c0 < s.width {
			s.out.Segs = append(s.out.Segs, s.line[s.segStart:cut])
			s.out.Spans = append(s.out.Spans, BackdropSpan{C0: c0, C1: s.width, Glass: s.fullGlass})
			s.segStart = len(s.line)
		}
		return
	}
	leftMargin := 1
	if s.blankCol0 == 0 {
		leftMargin = 0
	}
	c0 := s.blankCol0 + leftMargin
	c1 := min(s.col-1, s.width)
	if c1 <= c0 || c0 >= s.width {
		return
	}
	cutStart := s.blankPos0
	if leftMargin == 1 {
		cutStart = advanceOneSpace(s.line, s.blankPos0)
	}
	s.out.Segs = append(s.out.Segs, s.line[s.segStart:cutStart])
	s.out.Spans = append(s.out.Spans, BackdropSpan{C0: c0, C1: c1, Glass: true, Restore: s.blankSGR})
	s.segStart = s.blankPos1
}

func (s *bgLineScanner) finish() {
	if s.inBlank {
		s.flushBlank(true)
	} else if s.col < s.width {
		c0, pad := s.col, ""
		if s.fullGlass && s.col > 0 {
			c0 = s.col + 1
			pad = "\x1b[0m "
		}
		if c0 < s.width {
			s.out.Segs = append(s.out.Segs, s.line[s.segStart:]+pad)
			s.out.Spans = append(s.out.Spans, BackdropSpan{C0: c0, C1: s.width, Glass: s.fullGlass})
			s.segStart = len(s.line)
		}
	}
	if len(s.out.Spans) > 0 {
		s.out.Segs = append(s.out.Segs, s.line[s.segStart:])
	}
}

func advanceOneSpace(s string, pos int) int {
	for i := pos; i < len(s); {
		if s[i] == '\x1b' {
			end, _, _ := scanEscape(s, i)
			i = end
			continue
		}
		if s[i] == ' ' {
			return i + 1
		}
		return i
	}
	return len(s)
}

func scanEscape(s string, i int) (end int, isSGR bool, params string) {
	if i+1 >= len(s) {
		return len(s), false, ""
	}
	switch s[i+1] {
	case '[':
		j := i + 2
		for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
			j++
		}
		if j < len(s) {
			if s[j] == 'm' {
				return j + 1, true, s[i+2 : j]
			}
			return j + 1, false, ""
		}
		return len(s), false, ""
	case ']':
		j := i + 2
		for j < len(s) {
			if s[j] == '\x07' {
				return j + 1, false, ""
			}
			if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2, false, ""
			}
			j++
		}
		return len(s), false, ""
	default:
		return i + 2, false, ""
	}
}

const (
	maskBG uint8 = 1 << iota
	maskReverse
	maskUnderline
	maskStrike
)

func applySGRMask(mask uint8, params string) uint8 {
	if params == "" {
		return 0
	}
	for i := 0; i < len(params); {
		v, next := nextSGRInt(params, i)
		i = next
		switch {
		case v == 0:
			mask = 0
		case v == 4:
			mask |= maskUnderline
		case v == 7:
			mask |= maskReverse
		case v == 9:
			mask |= maskStrike
		case v == 24:
			mask &^= maskUnderline
		case v == 27:
			mask &^= maskReverse
		case v == 29:
			mask &^= maskStrike
		case (v >= 40 && v <= 47) || (v >= 100 && v <= 107):
			mask |= maskBG
		case v == 49:
			mask &^= maskBG
		case v == 48:
			mask |= maskBG
			i = skipExtColor(params, i)
		case v == 38 || v == 58:
			i = skipExtColor(params, i)
		}
	}
	return mask
}

func nextSGRInt(s string, i int) (int, int) {
	v := 0
	for i < len(s) && s[i] != ';' && s[i] != ':' {
		if s[i] >= '0' && s[i] <= '9' {
			v = v*10 + int(s[i]-'0')
		}
		i++
	}
	if i < len(s) {
		i++
	}
	return v, i
}

func skipExtColor(s string, i int) int {
	if i >= len(s) {
		return i
	}
	mode, next := nextSGRInt(s, i)
	switch mode {
	case 5:
		_, next = nextSGRInt(s, next)
	case 2:
		for range 3 {
			_, next = nextSGRInt(s, next)
		}
	}
	return next
}
