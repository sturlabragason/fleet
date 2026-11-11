package acphost

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// editor is the pane's input box (ADR-82): the text, the cursor in it,
// collapsed pastes and the history of sent lines. It has no lock and draws
// nothing; the view owns one and holds its lock around every call.
//
// buf may hold newlines (Alt-Enter, Shift-Enter, Ctrl-J) and paste runes:
// one rune per collapsed paste, pasteBase+i for pastes[i].
type editor struct {
	buf    []rune
	pos    int // cursor: an index into buf, 0..len(buf)
	pastes []string

	hist  []histEntry
	hidx  int       // the entry shown; len(hist) is the line being typed
	draft histEntry // the line being typed while history is shown
}

type histEntry struct {
	buf    []rune
	pastes []string
}

// maxHist bounds the history of sent lines.
const maxHist = 200

func (e *editor) empty() bool { return len(e.buf) == 0 }

func (e *editor) insert(rs ...rune) {
	e.buf = append(e.buf[:e.pos], append(rs, e.buf[e.pos:]...)...)
	e.pos += len(rs)
}

// paste inserts pasted text at the cursor, collapsed to one rune if it is
// more than one line or longer than pasteMax runes.
func (e *editor) paste(s string) {
	if s == "" {
		return
	}
	if !strings.Contains(s, "\n") && utf8.RuneCountInString(s) <= pasteMax {
		e.insert([]rune(s)...)
		return
	}
	e.insert(rune(pasteBase + len(e.pastes)))
	e.pastes = append(e.pastes, s)
}

// cut removes buf[i:j] and leaves the cursor at i.
func (e *editor) cut(i, j int) {
	i, j = max(i, 0), min(j, len(e.buf))
	if i >= j {
		return
	}
	e.buf = append(e.buf[:i], e.buf[j:]...)
	e.pos = i
	if len(e.buf) == 0 {
		e.pastes = nil
	}
}

func (e *editor) clear() { e.buf, e.pos, e.pastes = e.buf[:0], 0, nil }

func (e *editor) backspace() { e.cut(e.pos-1, e.pos) }
func (e *editor) del()       { e.cut(e.pos, e.pos+1) }
func (e *editor) left()      { e.pos = max(e.pos-1, 0) }
func (e *editor) right()     { e.pos = min(e.pos+1, len(e.buf)) }

func (e *editor) lineStart(i int) int {
	for i > 0 && e.buf[i-1] != '\n' {
		i--
	}
	return i
}

func (e *editor) lineEnd(i int) int {
	for i < len(e.buf) && e.buf[i] != '\n' {
		i++
	}
	return i
}

func (e *editor) home() { e.pos = e.lineStart(e.pos) }
func (e *editor) end()  { e.pos = e.lineEnd(e.pos) }

// wordRune is a rune a word jump stops around: letters, digits, _ and pastes.
func wordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || isPasteRune(r)
}

func (e *editor) wordBack(pos int, in func(rune) bool) int {
	for pos > 0 && !in(e.buf[pos-1]) {
		pos--
	}
	for pos > 0 && in(e.buf[pos-1]) {
		pos--
	}
	return pos
}

func (e *editor) wordFwd(pos int, in func(rune) bool) int {
	for pos < len(e.buf) && !in(e.buf[pos]) {
		pos++
	}
	for pos < len(e.buf) && in(e.buf[pos]) {
		pos++
	}
	return pos
}

func notSpace(r rune) bool { return !unicode.IsSpace(r) }

func (e *editor) wordLeft()  { e.pos = e.wordBack(e.pos, wordRune) }
func (e *editor) wordRight() { e.pos = e.wordFwd(e.pos, wordRune) }

// rubout is Ctrl-W: delete back to the previous whitespace.
func (e *editor) rubout() { e.cut(e.wordBack(e.pos, notSpace), e.pos) }

// delWordBack is Alt-Backspace, delWordFwd Alt-D.
func (e *editor) delWordBack() { e.cut(e.wordBack(e.pos, wordRune), e.pos) }
func (e *editor) delWordFwd()  { e.cut(e.pos, e.wordFwd(e.pos, wordRune)) }

// killBack is Ctrl-U: delete to the start of the line, or the line break
// before the cursor when it is already there.
func (e *editor) killBack() {
	s := e.lineStart(e.pos)
	if s == e.pos {
		s--
	}
	e.cut(s, e.pos)
}

// killFwd is Ctrl-K: delete to the end of the line, or the line break.
func (e *editor) killFwd() {
	n := e.lineEnd(e.pos)
	if n == e.pos {
		n++
	}
	e.cut(e.pos, n)
}

// up moves to the line above, or shows the previous sent line when the
// cursor is on the first line.
func (e *editor) up() {
	if s := e.lineStart(e.pos); s > 0 {
		ps := e.lineStart(s - 1)
		e.pos = min(ps+e.pos-s, s-1)
		return
	}
	if e.hidx == 0 {
		return
	}
	if e.hidx == len(e.hist) {
		e.draft = e.save()
	}
	e.hidx--
	e.load(e.hist[e.hidx])
}

// down moves to the line below, or to the next sent line, and back to the
// line being typed after the newest.
func (e *editor) down() {
	if n := e.lineEnd(e.pos); n < len(e.buf) {
		s := e.lineStart(e.pos)
		e.pos = min(n+1+e.pos-s, e.lineEnd(n+1))
		return
	}
	if e.hidx >= len(e.hist) {
		return
	}
	e.hidx++
	if e.hidx == len(e.hist) {
		e.load(e.draft)
		e.draft = histEntry{}
		return
	}
	e.load(e.hist[e.hidx])
}

func (e *editor) save() histEntry {
	return histEntry{buf: slices.Clone(e.buf), pastes: slices.Clone(e.pastes)}
}

func (e *editor) load(h histEntry) {
	e.buf = append(e.buf[:0], h.buf...)
	e.pastes = slices.Clone(h.pastes)
	e.pos = len(e.buf)
}

// text is what the box holds, pastes expanded.
func (e *editor) text() string {
	var b strings.Builder
	for _, r := range e.buf {
		if isPasteRune(r) {
			if i := int(r - pasteBase); i < len(e.pastes) {
				b.WriteString(e.pastes[i])
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// take empties the box, keeps what it held in the history and returns it,
// pastes expanded.
func (e *editor) take() string {
	s := e.text()
	if strings.TrimSpace(s) != "" {
		h := e.save()
		if n := len(e.hist); n == 0 || e.hist[n-1].text() != s {
			e.hist = append(e.hist, h)
			if len(e.hist) > maxHist {
				e.hist = e.hist[1:]
			}
		}
	}
	e.buf, e.pos, e.pastes = nil, 0, nil
	e.hidx, e.draft = len(e.hist), histEntry{}
	return s
}

func (h histEntry) text() string { return (&editor{buf: h.buf, pastes: h.pastes}).text() }

// pasteLabel is how collapsed paste rune r shows in the box.
func (e *editor) pasteLabel(r rune) string {
	i := int(r - pasteBase)
	if i < 0 || i >= len(e.pastes) {
		return ""
	}
	if n := strings.Count(strings.TrimRight(e.pastes[i], "\n"), "\n") + 1; n > 1 {
		return fmt.Sprintf("[Paste #%d +%d lines]", i+1, n)
	}
	return fmt.Sprintf("[Paste #%d %d chars]", i+1, utf8.RuneCountInString(e.pastes[i]))
}

// cell is one drawn rune of the box: its index in buf, text and width.
type cell struct {
	i, w int
	s    string
}

// row is one drawn row of the box; end is the index just past it.
type row struct {
	cells []cell
	end   int
}

func (r row) width() int {
	n := 0
	for _, c := range r.cells {
		n += c.w
	}
	return n
}

// layout wraps the box to rows of at most room columns and returns them
// with the cursor's row and column. A line break ends a row and is not
// drawn; a cell wider than room gets a row of its own.
func (e *editor) layout(room int) (rows []row, crow, ccol int) {
	room = max(room, 1)
	cur := row{}
	used := 0
	crow, ccol = -1, 0
	for i, r := range e.buf {
		if r == '\n' {
			cur.end = i
			rows = append(rows, cur)
			cur, used = row{}, 0
			continue
		}
		s := string(r)
		if isPasteRune(r) {
			s = e.pasteLabel(r)
		}
		w := ansi.StringWidth(s)
		if used+w > room && len(cur.cells) > 0 {
			cur.end = i
			rows = append(rows, cur)
			cur, used = row{}, 0
		}
		if i == e.pos {
			crow, ccol = len(rows), used
		}
		cur.cells = append(cur.cells, cell{i: i, w: w, s: s})
		used += w
	}
	cur.end = len(e.buf)
	rows = append(rows, cur)
	if crow < 0 {
		// The cursor is at a line break or the end: after its row's text.
		for k, rw := range rows {
			if rw.end == e.pos && (k+1 == len(rows) || e.buf[rw.end] == '\n') {
				crow, ccol = k, rw.width()
				break
			}
		}
	}
	return rows, crow, ccol
}

// click puts the cursor at column x of a row from layout: before the cell
// under x, after it when x is past the middle of a wide cell, or at the
// row's end.
func (e *editor) click(r row, x int) {
	col := 0
	for _, c := range r.cells {
		if x < col+(c.w+1)/2 {
			e.pos = c.i
			return
		}
		col += c.w
	}
	e.pos = r.end
}
