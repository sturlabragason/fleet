package acphost

import (
	"strings"
	"testing"
)

// ed builds an editor from s, "|" marking the cursor.
func ed(s string) *editor {
	i := strings.Index(s, "|")
	r := []rune(strings.Replace(s, "|", "", 1))
	return &editor{buf: r, pos: len([]rune(s[:i]))}
}

func (e *editor) String() string {
	return string(e.buf[:e.pos]) + "|" + string(e.buf[e.pos:])
}

func TestEditorKeys(t *testing.T) {
	cases := []struct {
		in   string
		fn   func(*editor)
		want string
	}{
		{"ab|cd", func(e *editor) { e.insert('X') }, "abX|cd"},
		{"ab|cd", (*editor).backspace, "a|cd"},
		{"|ab", (*editor).backspace, "|ab"},
		{"ab|cd", (*editor).del, "ab|d"},
		{"ab|", (*editor).del, "ab|"},
		{"ab|cd", (*editor).left, "a|bcd"},
		{"ab|cd", (*editor).right, "abc|d"},
		{"x\nab|cd\ny", (*editor).home, "x\n|abcd\ny"},
		{"x\nab|cd\ny", (*editor).end, "x\nabcd|\ny"},
		{"foo.bar baz|", (*editor).wordLeft, "foo.bar |baz"},
		{"foo.bar |baz", (*editor).wordLeft, "foo.|bar baz"},
		{"|foo.bar baz", (*editor).wordRight, "foo|.bar baz"},
		{"foo.bar baz  |", (*editor).rubout, "foo.bar |"},
		{"foo.bar|", (*editor).delWordBack, "foo.|"},
		{"|foo bar", (*editor).delWordFwd, "| bar"},
		{"x\nab|cd", (*editor).killBack, "x\n|cd"},
		{"x\n|cd", (*editor).killBack, "x|cd"},
		{"ab|cd\ny", (*editor).killFwd, "ab|\ny"},
		{"ab|\ny", (*editor).killFwd, "ab|y"},
		{"abc\nd|e", (*editor).up, "a|bc\nde"},
		{"a\nxyz|", (*editor).up, "a|\nxyz"},
		{"ab|c\nd", (*editor).down, "abc\nd|"},
	}
	for _, c := range cases {
		e := ed(c.in)
		c.fn(e)
		if got := e.String(); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEditorPasteAtCursorAndTake(t *testing.T) {
	e := ed("say | now")
	e.paste("one\ntwo")
	if got := e.String(); got != "say \U000F0000| now" {
		t.Fatalf("buf = %q", got)
	}
	e.backspace() // a collapsed paste goes whole
	e.paste("one\ntwo")
	e.left()
	e.insert('>')
	if got := e.take(); got != "say >one\ntwo now" {
		t.Fatalf("take = %q", got)
	}
	if !e.empty() || e.pos != 0 {
		t.Fatalf("after take: %q", e.String())
	}
}

func TestEditorHistory(t *testing.T) {
	e := &editor{}
	for _, s := range []string{"first", "second\nline", "second\nline"} {
		e.insert([]rune(s)...)
		e.take()
	}
	if len(e.hist) != 2 {
		t.Fatalf("hist = %d, want repeats kept once", len(e.hist))
	}
	e.insert([]rune("draft")...)
	e.up()
	if got := e.String(); got != "second\nline|" {
		t.Fatalf("up = %q", got)
	}
	e.up() // moves within the recalled line first
	e.up()
	if got := e.String(); got != "first|" {
		t.Fatalf("up up up = %q", got)
	}
	e.up()
	if got := e.String(); got != "first|" {
		t.Fatalf("past the oldest = %q", got)
	}
	e.down()
	e.down()
	e.down()
	if got := e.String(); got != "draft|" {
		t.Fatalf("back down = %q", got)
	}
}

func TestEditorLayoutAndClick(t *testing.T) {
	e := ed("abcdef\nxy|")
	rows, crow, ccol := e.layout(4)
	var got []string
	for _, r := range rows {
		var b strings.Builder
		for _, c := range r.cells {
			b.WriteString(c.s)
		}
		got = append(got, b.String())
	}
	if strings.Join(got, "/") != "abcd/ef/xy" || crow != 2 || ccol != 2 {
		t.Fatalf("rows %q cursor %d,%d", got, crow, ccol)
	}
	e.click(rows[1], 1)
	if e.String() != "abcde|f\nxy" {
		t.Fatalf("click = %q", e.String())
	}
	e.click(rows[1], 9) // past the end of a row ending in a line break
	if e.String() != "abcdef|\nxy" {
		t.Fatalf("click past end = %q", e.String())
	}
	if _, crow, ccol := e.layout(4); crow != 1 || ccol != 2 {
		t.Fatalf("cursor at break = %d,%d", crow, ccol)
	}
}
