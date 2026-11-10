package acphost

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// seg is a run of text in one style.
type seg struct {
	text string
	st   sty
}

// segLine is one drawn line: its segments, and whether it is a band whose
// background fills the pane width.
type segLine struct {
	segs []seg
	band string
}

func segWidth(ss []seg) int {
	n := 0
	for _, s := range ss {
		n += ansi.StringWidth(s.text)
	}
	return n
}

// wrap breaks segs into lines no wider than w: words move to the next line
// whole, a word wider than a line is cut. first prefixes the first line,
// rest the others (a hanging indent).
func wrap(segs []seg, w int, first, rest []seg) [][]seg {
	if w < 1 {
		w = 1
	}
	var out [][]seg
	line := append([]seg(nil), first...)
	used := segWidth(first)
	start := used // width of the prefix: nothing written after it yet
	newLine := func() {
		out = append(out, trimRight(line))
		line = append([]seg(nil), rest...)
		used = segWidth(rest)
		start = used
	}
	put := func(text string, st sty) {
		if n := len(line); n > 0 && line[n-1].st == st {
			line[n-1].text += text
		} else {
			line = append(line, seg{text, st})
		}
		used += ansi.StringWidth(text)
	}
	for _, s := range segs {
		for _, tok := range tokens(s.text) {
			tw := ansi.StringWidth(tok)
			if tok[0] == ' ' {
				if used == start {
					continue // no leading spaces on a wrapped line
				}
				if used+tw > w {
					newLine()
					continue
				}
				put(tok, s.st)
				continue
			}
			if used+tw > w && used > start {
				newLine()
			}
			for used+tw > w { // longer than a whole line: cut it
				room := w - used
				if room < 1 {
					room = 1
				}
				head := cutWidth(tok, room)
				put(head, s.st)
				tok = tok[len(head):]
				tw = ansi.StringWidth(tok)
				newLine()
			}
			if tok != "" {
				put(tok, s.st)
			}
		}
	}
	if used > start || len(out) == 0 {
		out = append(out, trimRight(line))
	}
	return out
}

// tokens splits s into runs of spaces and runs of anything else.
func tokens(s string) []string {
	var out []string
	for s != "" {
		sp := s[0] == ' '
		i := 0
		for i < len(s) && (s[i] == ' ') == sp {
			i++
		}
		out = append(out, s[:i])
		s = s[i:]
	}
	return out
}

// cutWidth returns the longest prefix of s at most w columns wide, and at
// least one rune.
func cutWidth(s string, w int) string {
	n, i := 0, 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		rw := ansi.StringWidth(string(r))
		if n+rw > w && i > 0 {
			break
		}
		n += rw
		i += size
	}
	return s[:i]
}

func trimRight(l []seg) []seg {
	for len(l) > 0 {
		t := strings.TrimRight(l[len(l)-1].text, " ")
		if t != "" {
			l[len(l)-1].text = t
			return l
		}
		l = l[:len(l)-1]
	}
	return l
}

// mdStyles are the looks markdown maps to.
type mdStyles struct {
	body, code, link, url, quote, rule sty
	keepTicks                          bool // no colour: inline code keeps its backticks
}

func (v *view) md() mdStyles {
	pl := v.pal
	return mdStyles{
		body:      sty{fg: pl.text},
		code:      sty{fg: pl.sub, bg: pl.band},
		link:      sty{fg: pl.text, under: true},
		url:       sty{fg: pl.dim},
		quote:     sty{fg: pl.sub, italic: true},
		rule:      sty{fg: pl.rule},
		keepTicks: !v.pt.color(),
	}
}

// markdown renders s, an agent message, as wrapped lines w columns wide
// with indent spaces before each: headings, bold, italic, inline code,
// links, bullet and numbered lists, quotes, rules and fenced code.
func markdown(s string, w, indent int, ms mdStyles) [][]seg {
	pad := strings.Repeat(" ", indent)
	var out [][]seg
	fence := false
	for _, raw := range strings.Split(strings.TrimRight(s, "\n "), "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trim := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			fence = !fence
			continue
		}
		if fence {
			code := strings.ReplaceAll(line, "\t", "    ")
			body := strings.TrimLeft(code, " ")
			in := []seg{{pad + "  " + strings.Repeat(" ", min(len(code)-len(body), w/2)), sty{}}}
			out = append(out, wrap([]seg{{body, ms.code}}, w, in, in)...)
			continue
		}
		if trim == "" {
			out = append(out, nil)
			continue
		}
		lead := pad + strings.Repeat(" ", len(line)-len(trim))
		switch {
		case isRule(trim):
			n := w - indent
			if n > 40 {
				n = 40
			}
			out = append(out, []seg{{pad, sty{}}, {strings.Repeat("─", max(n, 1)), ms.rule}})
		case heading(trim) > 0:
			text := strings.TrimSpace(strings.TrimLeft(trim, "#"))
			st := ms.body
			st.bold = true
			st.under = heading(trim) == 1
			out = append(out, wrap(inline(text, st, ms), w, []seg{{lead, sty{}}}, []seg{{lead, sty{}}})...)
		case strings.HasPrefix(trim, "> ") || trim == ">":
			text := strings.TrimPrefix(strings.TrimPrefix(trim, ">"), " ")
			bar := []seg{{lead, sty{}}, {"│ ", ms.url}}
			out = append(out, wrap(inline(text, ms.quote, ms), w, bar, bar)...)
		default:
			marker, text := listItem(trim)
			if marker == "" {
				out = append(out, wrap(inline(trim, ms.body, ms), w, []seg{{lead, sty{}}}, []seg{{lead, sty{}}})...)
				break
			}
			hang := lead + strings.Repeat(" ", ansi.StringWidth(marker))
			out = append(out, wrap(inline(text, ms.body, ms), w,
				[]seg{{lead, sty{}}, {marker, ms.url}}, []seg{{hang, sty{}}})...)
		}
	}
	return out
}

func isRule(s string) bool {
	s = strings.ReplaceAll(s, " ", "")
	if len(s) < 3 {
		return false
	}
	return strings.Trim(s, "-") == "" || strings.Trim(s, "*") == "" || strings.Trim(s, "_") == ""
}

// heading is the level of a "# " heading line, or 0.
func heading(s string) int {
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n >= len(s) || s[n] != ' ' {
		return 0
	}
	return n
}

// listItem splits a list line into its drawn marker ("• ", "3. ") and text.
func listItem(s string) (string, string) {
	if len(s) >= 2 && (s[0] == '-' || s[0] == '*' || s[0] == '+') && s[1] == ' ' {
		text := strings.TrimLeft(s[2:], " ")
		switch {
		case strings.HasPrefix(text, "[ ] "):
			return "☐ ", text[4:]
		case strings.HasPrefix(text, "[x] "), strings.HasPrefix(text, "[X] "):
			return "☑ ", text[4:]
		}
		return "• ", text
	}
	i := 0
	for i < len(s) && i < 4 && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(s) && (s[i] == '.' || s[i] == ')') && s[i+1] == ' ' {
		return s[:i+1] + " ", strings.TrimLeft(s[i+2:], " ")
	}
	return "", s
}

// inline renders one line's spans: `code`, **bold**, *italic*, _italic_,
// [text](url). An opening mark without a closing one is left as text.
func inline(s string, base sty, ms mdStyles) []seg {
	var out []seg
	var cur strings.Builder
	st := base
	bold, ital := false, byte(0)
	emit := func() {
		if cur.Len() > 0 {
			out = append(out, seg{cur.String(), st})
			cur.Reset()
		}
	}
	for i := 0; i < len(s); {
		rest := s[i:]
		switch {
		case rest[0] == '`':
			n := 1
			for n < len(rest) && rest[n] == '`' {
				n++
			}
			ticks := rest[:n]
			if j := strings.Index(rest[n:], ticks); j >= 0 {
				emit()
				code := rest[n : n+j]
				if ms.keepTicks {
					code = "`" + strings.TrimSpace(code) + "`"
				}
				cs := ms.code
				cs.bold = st.bold
				out = append(out, seg{code, cs})
				i += n + j + n
				continue
			}
		case strings.HasPrefix(rest, "**"):
			if bold {
				emit()
				st.bold, bold = base.bold, false
				i += 2
				continue
			}
			if len(rest) > 2 && rest[2] != ' ' && strings.Contains(rest[3:], "**") {
				emit()
				st.bold, bold = true, true
				i += 2
				continue
			}
		case rest[0] == '*' || rest[0] == '_':
			if ital == rest[0] && i > 0 && s[i-1] != ' ' {
				emit()
				st.italic, ital = base.italic, 0
				i++
				continue
			}
			if ital == 0 && opensItalic(s, i) {
				emit()
				st.italic, ital = true, rest[0]
				i++
				continue
			}
		case rest[0] == '[':
			if text, url, n, ok := link(rest); ok {
				emit()
				ls := ms.link
				ls.bold = st.bold
				out = append(out, seg{text, ls})
				if url != text && url != "" {
					out = append(out, seg{" (" + url + ")", ms.url})
				}
				i += n
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(rest)
		cur.WriteRune(r)
		i += size
	}
	emit()
	return out
}

// opensItalic reports whether the * or _ at s[i] opens an italic span: it
// is followed by a non-space, not preceded by a letter or digit (so
// snake_case stays) and the same mark closes it later on the line.
func opensItalic(s string, i int) bool {
	m := s[i]
	if i+1 >= len(s) || s[i+1] == ' ' || s[i+1] == m {
		return false
	}
	if i > 0 && isWord(s[:i], true) {
		return false
	}
	for j := i + 2; j < len(s); j++ {
		if s[j] == m && s[j-1] != ' ' && (j+1 == len(s) || !isWord(s[j+1:], false)) {
			return true
		}
	}
	return false
}

// isWord reports whether the rune at the end (last) or start of s is a
// letter or digit.
func isWord(s string, last bool) bool {
	var r rune
	if last {
		r, _ = utf8.DecodeLastRuneInString(s)
	} else {
		r, _ = utf8.DecodeRuneInString(s)
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// link parses "[text](url)" at the start of s.
func link(s string) (text, url string, n int, ok bool) {
	end := strings.Index(s, "](")
	if end < 1 || strings.Contains(s[1:end], "[") {
		return "", "", 0, false
	}
	close := strings.IndexByte(s[end+2:], ')')
	if close < 0 {
		return "", "", 0, false
	}
	return s[1:end], s[end+2 : end+2+close], end + 2 + close + 1, true
}
