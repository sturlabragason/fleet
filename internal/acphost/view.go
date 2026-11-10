package acphost

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// The pane when it is a terminal (ADR-82, ADR-84): a header with the
// agent's name, profile and status, the conversation, a rule, the input
// line and a line of key hints. The whole screen is drawn again on every
// change, from the conversation kept as blocks, so a tool call is one line
// whose status changes in place and every line wraps to the pane's width
// and reflows when it is resized. Nothing blinks: glyphs and colours are
// static and the cursor is a steady bar.

// Terminal modes the pane sets on start and resets on exit.
const (
	// ScreenOn switches to the alternate screen, so the drawn frames never
	// pile up in tmux's history, and makes the cursor a steady bar.
	ScreenOn = "\x1b[?1049h\x1b[6 q\x1b[?25h"
	// ScreenOff restores the cursor and the normal screen.
	ScreenOff = "\x1b[0 q\x1b[?1049l"
)

// maxBlocks bounds what the pane keeps; the transcript has everything.
const maxBlocks = 1000

// block is one thing in the conversation: a prompt, a message, a tool call.
type block struct {
	kind                string
	text, title, status string
	sub, answer         string
	options             []string
	w                   int // width lines was drawn for; 0 means draw again
	lines               []segLine
}

// view is the pane's writer when the host reads keys.
type view struct {
	mu   sync.Mutex
	out  io.Writer
	size func() (int, int)
	pt   painter
	pal  palette

	name, profile, status string
	blocks                []*block
	tools                 map[string]*block // tool call id → its line
	asks                  map[string]*block // tool call id → its open question
	buf                   []rune
	scroll                int // lines scrolled back from the bottom
	page                  int // body height at the last draw, for PgUp
}

func newView(out io.Writer, size func() (int, int), p colorprofile.Profile, name, profile string) *view {
	return &view{out: out, size: size, pt: painter{p}, pal: themePalette(),
		name: name, profile: profile, tools: map[string]*block{}, asks: map[string]*block{}}
}

// add puts an entry into the conversation, or updates the block it belongs
// to (a tool call's status, a question's answer), and draws the screen.
func (v *view) add(e Entry) {
	e.Text, e.Title, e.Sub, e.SessionID = clean(e.Text), clean(e.Title), clean(e.Sub), clean(e.SessionID)
	if len(e.Options) > 0 {
		e.Options = append(e.Options[:0:0], e.Options...)
		for i := range e.Options {
			e.Options[i].Name = clean(e.Options[i].Name)
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	switch e.Kind {
	case KindStatus:
		v.status = e.Status
	case KindUsage:
		return
	case KindTool:
		if b := v.tools[e.ToolID]; b != nil && e.ToolID != "" {
			if e.Title != "" {
				b.title = e.Title
			}
			if e.Status != "" {
				b.status = e.Status
			}
			if e.Sub != "" {
				b.sub = e.Sub
			}
			b.w = 0
			break
		}
		b := &block{kind: KindTool, title: e.Title, status: e.Status, sub: e.Sub}
		v.push(b)
		if e.ToolID != "" {
			v.tools[e.ToolID] = b
		}
	case KindPermission:
		b := &block{kind: KindPermission, title: e.Title}
		for _, o := range e.Options {
			b.options = append(b.options, o.Name)
		}
		v.push(b)
		v.asks[e.ToolID] = b
	case KindAnswer:
		if b := v.asks[e.ToolID]; b != nil {
			b.answer, b.w = e.Text, 0
			delete(v.asks, e.ToolID)
			break
		}
		v.push(&block{kind: KindAnswer, text: e.Text})
	default:
		v.push(&block{kind: e.Kind, text: e.Text, title: e.SessionID})
	}
	v.draw()
}

// clean makes agent text safe to measure and draw: a tab becomes four
// spaces, a CRLF a newline, and other control bytes (ESC, CR, BEL, DEL) are
// dropped, as the terminal would act on them while the width math counts
// them as nothing.
func clean(s string) string {
	ctl := func(r rune) bool { return (r < 0x20 && r != '\n') || r == 0x7f || (r >= 0x80 && r < 0xa0) }
	if !strings.ContainsFunc(s, ctl) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteString("    ")
		case !ctl(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// note prints a line of the host's own: "! ..." is an error.
func (v *view) note(s string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.push(&block{kind: "note", text: clean(s)})
	v.draw()
}

// Write is io.Writer: each line is a note.
func (v *view) Write(p []byte) (int, error) {
	for _, l := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		v.note(l)
	}
	return len(p), nil
}

// push adds b and drops the oldest blocks past maxBlocks. Caller holds mu.
func (v *view) push(b *block) {
	v.blocks = append(v.blocks, b)
	if len(v.blocks) <= maxBlocks {
		return
	}
	old := v.blocks[0]
	v.blocks = v.blocks[1:]
	for id, t := range v.tools {
		if t == old {
			delete(v.tools, id)
		}
	}
	for id, t := range v.asks {
		if t == old {
			delete(v.asks, id)
		}
	}
}

// edit changes the input line and draws the screen.
func (v *view) edit(fn func(buf []rune) []rune) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.buf = fn(v.buf)
	v.draw()
}

// take empties the input line and returns what it held.
func (v *view) take() string {
	var s string
	v.edit(func(buf []rune) []rune { s = string(buf); return buf[:0] })
	return s
}

func (v *view) empty() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.buf) == 0
}

// scrollBy moves the conversation by n pages: positive is back in time.
func (v *view) scrollBy(pages int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	step := v.page / 2
	if step < 1 {
		step = 1
	}
	v.scroll += pages * step
	if v.scroll < 0 {
		v.scroll = 0
	}
	v.draw()
}

// redraw draws the screen again, after a resize.
func (v *view) redraw() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.draw()
}

func (v *view) dims() (int, int) {
	w, h := 80, 24
	if v.size != nil {
		if a, b := v.size(); a > 0 && b > 0 {
			w, h = a, b
		}
	}
	return w, h
}

// draw writes the whole frame, inside one synchronized update so the
// terminal shows it at once. Caller holds mu.
func (v *view) draw() {
	w, h := v.dims()
	rows, cx, cy := v.frame(w, h)
	var b strings.Builder
	b.WriteString("\x1b[?2026h")
	for i, r := range rows {
		fmt.Fprintf(&b, "\x1b[%d;1H%s", i+1, r)
		// Erasing after a full-width line would eat its last cell.
		if ansi.StringWidth(r) < w {
			b.WriteString("\x1b[K")
		}
	}
	fmt.Fprintf(&b, "\x1b[%d;%dH\x1b[?2026l", cy, cx)
	_, _ = io.WriteString(v.out, b.String())
}

// frame lays the screen out as h styled rows and returns where the cursor
// goes (1-based column, row). Caller holds mu.
func (v *view) frame(w, h int) (rows []string, cx, cy int) {
	var top, bottom []string
	if h >= 3 {
		top = append(top, v.header(w))
	}
	var hint string
	if h >= 8 {
		hint = v.hint(w)
	}
	input, cx := v.input(w)
	if h >= 6 {
		bottom = append(bottom, "") // the rule, drawn once the scroll is known
	}
	bottom = append(bottom, input)
	if hint != "" {
		bottom = append(bottom, hint)
	}
	bodyH := h - len(top) - len(bottom)
	if bodyH < 0 {
		bodyH = 0
	}
	v.page = bodyH
	body, more := v.body(w, bodyH)
	if h >= 6 {
		bottom[0] = v.rule(w, more)
	}
	rows = append(rows, top...)
	rows = append(rows, body...)
	for len(rows) < h-len(bottom) {
		rows = append(rows, "")
	}
	rows = append(rows, bottom...)
	if len(rows) > h {
		rows = rows[len(rows)-h:]
	}
	cy = len(rows)
	if hint != "" {
		cy--
	}
	return rows, cx, cy
}

// body is the conversation's last n lines, v.scroll lines back, and how
// many newer lines are below them. Caller holds mu.
func (v *view) body(w, n int) ([]string, int) {
	if n <= 0 {
		return nil, 0
	}
	want := n + v.scroll
	var rev []segLine // newest first
	for i := len(v.blocks) - 1; i >= 0 && len(rev) < want; i-- {
		b := v.blocks[i]
		ls := v.lines(b, w)
		for j := len(ls) - 1; j >= 0; j-- {
			rev = append(rev, ls[j])
		}
		if i > 0 && !(compact(b.kind) && compact(v.blocks[i-1].kind)) {
			rev = append(rev, segLine{})
		}
	}
	if v.scroll > len(rev)-n {
		v.scroll = max(len(rev)-n, 0)
	}
	end := len(rev)
	if end > v.scroll+n {
		end = v.scroll + n
	}
	var out []string
	for j := end - 1; j >= v.scroll; j-- {
		out = append(out, v.paintLine(rev[j], w))
	}
	return out, v.scroll
}

// compact kinds sit on consecutive lines; anything else gets a blank line.
func compact(kind string) bool {
	switch kind {
	case KindTool, KindAnswer, KindCancel, KindSession, "note":
		return true
	}
	return false
}

func (v *view) paintLine(l segLine, w int) string {
	var b strings.Builder
	for _, s := range l.segs {
		st := s.st
		if l.band != "" && st.bg == "" {
			st.bg = l.band
		}
		b.WriteString(v.pt.paint(st, s.text))
	}
	if l.band != "" && v.pt.color() {
		if pad := w - segWidth(l.segs); pad > 0 {
			b.WriteString(v.pt.paint(sty{bg: l.band}, strings.Repeat(" ", pad)))
		}
	}
	return b.String()
}

// lines draws b for width w, cached until b or w changes. Caller holds mu.
func (v *view) lines(b *block, w int) []segLine {
	if b.w == w && b.lines != nil {
		return b.lines
	}
	b.w, b.lines = w, nil
	pl := v.pal
	add := func(ls [][]seg, band string) {
		for _, l := range ls {
			b.lines = append(b.lines, segLine{segs: l, band: band})
		}
	}
	ind := func(n int) []seg { return []seg{{strings.Repeat(" ", n), sty{}}} }
	switch b.kind {
	case KindUser:
		you := sty{fg: pl.you, bold: true}
		text := sty{fg: pl.text, bold: true}
		band := pl.band
		var ls [][]seg
		for i, para := range strings.Split(strings.TrimRight(b.text, "\n "), "\n") {
			first := []seg{{" ", sty{}}, {glyphYou + " ", you}}
			if i > 0 {
				first = ind(3)
			}
			ls = append(ls, wrap([]seg{{para, text}}, w, first, ind(3))...)
		}
		add(ls, band)
	case KindAgent:
		add(markdown(b.text, w, 2, v.md()), "")
	case KindThought:
		text := strings.Join(strings.Fields(b.text), " ")
		st := sty{fg: pl.dim, italic: true}
		ls := wrap([]seg{{text, st}}, w-1, append(ind(2), seg{glyphThought + " ", sty{fg: pl.faint}}), ind(4))
		if len(ls) > 2 {
			ls = ls[:2]
			ls[1] = append(ls[1], seg{"…", st})
		}
		add(ls, "")
	case KindTool:
		g, c := pl.tool(b.status)
		title := firstLine(b.title)
		if title == "" {
			title = "tool call"
		}
		segs := []seg{{title, sty{fg: pl.sub}}}
		if b.sub != "" {
			segs = append(segs, seg{" → " + b.sub, sty{fg: pl.dim}})
		}
		if b.status == "failed" {
			segs = append(segs, seg{" failed", sty{fg: pl.err}})
		}
		ls := wrap(segs, w-1, append(ind(2), seg{g + " ", sty{fg: c, bold: true}}), ind(4))
		if len(ls) > 2 {
			ls = ls[:2]
			ls[1] = append(ls[1], seg{"…", sty{fg: pl.dim}})
		}
		add(ls, "")
	case KindPermission:
		if b.answer != "" {
			add(wrap([]seg{{firstLine(b.title), sty{fg: pl.dim}}, {" → " + b.answer, sty{fg: pl.text}}}, w,
				append(ind(2), seg{glyphAsk + " ", sty{fg: pl.dim, bold: true}}), ind(4)), "")
			break
		}
		add(wrap([]seg{{firstLine(b.title), sty{fg: pl.text, bold: true}}}, w,
			append(ind(2), seg{glyphAsk + " ", sty{fg: pl.waiting, bold: true}}), ind(4)), "")
		for i, o := range b.options {
			n := strconv.Itoa(i + 1)
			add(wrap([]seg{{o, sty{fg: pl.text}}}, w,
				append(ind(4), seg{n, sty{fg: pl.waiting, bold: true}}, seg{"  ", sty{}}), ind(4+len(n)+2)), "")
		}
	case KindAnswer:
		add(wrap([]seg{{"→ " + b.text, sty{fg: pl.dim}}}, w, ind(4), ind(6)), "")
	case KindCancel:
		add(wrap([]seg{{"⊘ cancel sent", sty{fg: pl.dim}}}, w, ind(2), ind(4)), "")
	case KindSession:
		add(wrap([]seg{{glyphNote + " session " + b.title + " " + b.text, sty{fg: pl.faint}}}, w, ind(2), ind(4)), "")
	case KindError:
		v.errorLines(b, w, strings.TrimRight(b.text, "\n "), add)
	case "note":
		if rest, ok := strings.CutPrefix(b.text, "! "); ok {
			v.errorLines(b, w, rest, add)
			break
		}
		add(wrap([]seg{{b.text, sty{fg: pl.dim}}}, w, append(ind(2), seg{glyphNote + " ", sty{fg: pl.faint}}), ind(4)), "")
	}
	if b.lines == nil {
		b.lines = []segLine{}
	}
	return b.lines
}

func (v *view) errorLines(b *block, w int, text string, add func([][]seg, string)) {
	pl := v.pal
	for i, para := range strings.Split(text, "\n") {
		first := []seg{{"  ", sty{}}, {glyphFailed + " ", sty{fg: pl.err, bold: true}}}
		if i > 0 {
			first = []seg{{"    ", sty{}}}
		}
		add(wrap([]seg{{strings.TrimLeft(para, " "), sty{fg: pl.err}}}, w, first, []seg{{"    ", sty{}}}), "")
	}
}

// header is the top row: name and profile on the left, status on the right.
func (v *view) header(w int) string {
	pl := v.pal
	st := v.status
	if st == "" {
		st = "starting"
	}
	right := []seg{{statusGlyph(st) + " " + st + " ", sty{fg: pl.status(st), bold: true}}}
	left := []seg{{" " + v.name, sty{fg: pl.text, bold: true}}}
	if v.profile != "" {
		left = append(left, seg{" · " + v.profile, sty{fg: pl.dim}})
	}
	room := w - segWidth(right) - 1
	if room < 1 {
		return v.paintLine(segLine{segs: clipSegs(right, w), band: pl.head}, w)
	}
	left = clipSegs(left, room)
	gap := w - segWidth(left) - segWidth(right)
	l := append(left, seg{strings.Repeat(" ", max(gap, 1)), sty{}})
	return v.paintLine(segLine{segs: append(l, right...), band: pl.head}, w)
}

// clipSegs cuts segs to w columns, ending in "…" when cut.
func clipSegs(ss []seg, w int) []seg {
	if segWidth(ss) <= w {
		return ss
	}
	var out []seg
	used := 0
	for _, s := range ss {
		sw := ansi.StringWidth(s.text)
		if used+sw <= w-1 {
			out = append(out, s)
			used += sw
			continue
		}
		if room := w - 1 - used; room > 0 {
			out = append(out, seg{ansi.Truncate(s.text, room, ""), s.st})
		}
		out = append(out, seg{"…", s.st})
		break
	}
	return out
}

// rule is the line above the input; scrolled back it says how to return.
func (v *view) rule(w, more int) string {
	pl := v.pal
	if more == 0 {
		return v.pt.paint(sty{fg: pl.rule}, strings.Repeat("─", w))
	}
	label := fmt.Sprintf(" ↓ %d newer lines · PgDn ", more)
	n := w - ansi.StringWidth(label) - 2
	if n < 0 {
		return v.pt.paint(sty{fg: pl.waiting}, ansi.Truncate(label, w, ""))
	}
	return v.pt.paint(sty{fg: pl.rule}, "──") + v.pt.paint(sty{fg: pl.waiting}, label) +
		v.pt.paint(sty{fg: pl.rule}, strings.Repeat("─", n))
}

// input is the input line, clipped from the left to the pane width, and
// the cursor's column.
func (v *view) input(w int) (string, int) {
	pw := ansi.StringWidth(inputPrompt)
	room := w - pw - 1
	if room < 1 {
		room = 1
	}
	shown := strings.ReplaceAll(string(v.buf), "\n", "↵")
	for ansi.StringWidth(shown) > room {
		_, n := utf8.DecodeRuneInString(shown)
		shown = shown[n:]
	}
	s := v.pt.paint(sty{fg: v.pal.you, bold: true}, inputPrompt) + v.pt.paint(sty{fg: v.pal.text}, shown)
	return s, pw + ansi.StringWidth(shown) + 1
}

// hint is the key line under the input; what Enter does follows the state.
func (v *view) hint(w int) string {
	pl := v.pal
	enter := "send"
	if v.status == "working" {
		enter = "steer"
	}
	items := [][2]string{{"Enter", enter}, {"Esc", "cancel turn"}, {"Ctrl-T", "take over"}, {"PgUp/PgDn", "scroll"}}
	if len(v.asks) > 0 {
		items = append([][2]string{{"1-9", "answer"}}, items...)
	}
	for len(items) > 0 {
		segs := []seg{{" ", sty{}}}
		for i, it := range items {
			if i > 0 {
				segs = append(segs, seg{" · ", sty{fg: pl.faint}})
			}
			segs = append(segs, seg{it[0], sty{fg: pl.sub}}, seg{" " + it[1], sty{fg: pl.dim}})
		}
		if segWidth(segs) <= w {
			return v.paintLine(segLine{segs: segs}, w)
		}
		items = items[:len(items)-1]
	}
	return ""
}
