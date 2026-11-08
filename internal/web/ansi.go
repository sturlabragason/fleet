package web

import (
	"fmt"
	"html"
	"strconv"
	"strings"
)

// ANSI to HTML for pane captures (tmux capture-pane -e). tmux emits SGR
// only; any other escape is dropped.

// basic16 are the 16 standard terminal colours (xterm defaults).
var basic16 = [16]string{
	"#000000", "#cd0000", "#00cd00", "#cdcd00", "#0000ee", "#cd00cd", "#00cdcd", "#e5e5e5",
	"#7f7f7f", "#ff0000", "#00ff00", "#ffff00", "#5c5cff", "#ff00ff", "#00ffff", "#ffffff",
}

func color256(n int) string {
	switch {
	case n < 0 || n > 255:
		return ""
	case n < 16:
		return basic16[n]
	case n < 232:
		n -= 16
		lv := func(v int) int {
			if v == 0 {
				return 0
			}
			return 55 + v*40
		}
		return fmt.Sprintf("#%02x%02x%02x", lv(n/36), lv(n/6%6), lv(n%6))
	}
	g := 8 + (n-232)*10
	return fmt.Sprintf("#%02x%02x%02x", g, g, g)
}

type sgr struct {
	fg, bg                          string
	bold, faint, italic, under, rev bool
}

// apply folds one SGR parameter list ("1;38;5;196") into s.
func (s sgr) apply(params string) sgr {
	if params == "" {
		return sgr{}
	}
	p := strings.Split(params, ";")
	num := func(i int) int {
		if i >= len(p) {
			return -1
		}
		n, err := strconv.Atoi(p[i])
		if err != nil {
			return -1
		}
		return n
	}
	// ext reads 38/48 extended colours starting at p[i+1]; returns the colour
	// and how many extra params it used.
	ext := func(i int) (string, int) {
		switch num(i + 1) {
		case 5:
			return color256(num(i + 2)), 2
		case 2:
			r, g, b := num(i+2), num(i+3), num(i+4)
			if r < 0 || g < 0 || b < 0 || r > 255 || g > 255 || b > 255 {
				return "", 4
			}
			return fmt.Sprintf("#%02x%02x%02x", r, g, b), 4
		}
		return "", 0
	}
	for i := 0; i < len(p); i++ {
		switch n := num(i); {
		case n == 0 || p[i] == "":
			s = sgr{}
		case n == 1:
			s.bold = true
		case n == 2:
			s.faint = true
		case n == 3:
			s.italic = true
		case n == 4:
			s.under = true
		case n == 7:
			s.rev = true
		case n == 22:
			s.bold, s.faint = false, false
		case n == 23:
			s.italic = false
		case n == 24:
			s.under = false
		case n == 27:
			s.rev = false
		case n >= 30 && n <= 37:
			s.fg = basic16[n-30]
		case n >= 90 && n <= 97:
			s.fg = basic16[n-90+8]
		case n == 39:
			s.fg = ""
		case n >= 40 && n <= 47:
			s.bg = basic16[n-40]
		case n >= 100 && n <= 107:
			s.bg = basic16[n-100+8]
		case n == 49:
			s.bg = ""
		case n == 38:
			c, k := ext(i)
			s.fg, i = c, i+k
		case n == 48:
			c, k := ext(i)
			s.bg, i = c, i+k
		}
	}
	return s
}

func (s sgr) style() string {
	fg, bg := s.fg, s.bg
	if s.rev {
		if fg == "" {
			fg = "var(--fg)"
		}
		if bg == "" {
			bg = "var(--bg)"
		}
		fg, bg = bg, fg
	}
	var b strings.Builder
	if fg != "" {
		b.WriteString("color:" + fg + ";")
	}
	if bg != "" {
		b.WriteString("background:" + bg + ";")
	}
	if s.bold {
		b.WriteString("font-weight:bold;")
	}
	if s.faint {
		b.WriteString("opacity:.6;")
	}
	if s.italic {
		b.WriteString("font-style:italic;")
	}
	if s.under {
		b.WriteString("text-decoration:underline;")
	}
	return b.String()
}

// ANSIToHTML turns a capture with SGR escapes into escaped HTML with
// inline-styled spans. Every other escape sequence is dropped.
func ANSIToHTML(in string) string {
	var b strings.Builder
	var cur sgr
	open := "" // style of the span currently open
	text := func(t string) {
		if want := cur.style(); want != open {
			if open != "" {
				b.WriteString("</span>")
			}
			if want != "" {
				b.WriteString(`<span style="` + want + `">`)
			}
			open = want
		}
		b.WriteString(html.EscapeString(t))
	}
	for i := 0; i < len(in); {
		if in[i] != 0x1b {
			j := strings.IndexByte(in[i:], 0x1b)
			if j < 0 {
				j = len(in) - i
			}
			text(in[i : i+j])
			i += j
			continue
		}
		if i+1 >= len(in) {
			break
		}
		switch in[i+1] {
		case '[': // CSI: params, then a final byte 0x40-0x7e
			j := i + 2
			for j < len(in) && (in[j] < 0x40 || in[j] > 0x7e) {
				j++
			}
			if j < len(in) && in[j] == 'm' {
				cur = cur.apply(in[i+2 : j])
			}
			i = j + 1
		case ']', 'P', '_': // OSC/DCS/APC: up to BEL or ST
			j := i + 2
			for j < len(in) && in[j] != 0x07 && !(in[j] == 0x1b && j+1 < len(in) && in[j+1] == '\\') {
				j++
			}
			if j < len(in) && in[j] == 0x1b {
				j++
			}
			i = j + 1
		default:
			i += 2
		}
	}
	if open != "" {
		b.WriteString("</span>")
	}
	return b.String()
}
