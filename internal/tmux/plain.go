package tmux

import (
	"strconv"
	"strings"
)

// Plain turns `capture-pane -e` output into plain text and drops ghost text:
// a faint (SGR 2) run that nothing but spaces follows on its line. Typed
// input is never faint, while a suggestion or placeholder in an input box is,
// so the box reads as empty. Faint text followed by normal text on the same
// line (Copilot's transcript bar, Claude's diff line numbers) is kept.
func Plain(s string) string {
	out := make([]byte, 0, len(s))
	faint := false
	ghost := -1 // out offset where the line's trailing faint run starts
	endLine := func() {
		if ghost >= 0 {
			out = out[:ghost]
		}
		ghost = -1
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == 0x1b && i+1 < len(s) && s[i+1] == '[':
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				faint = sgrFaint(s[i+2:j], faint)
			}
			i = j
		case c == 0x1b && i+1 < len(s) && s[i+1] == ']': // OSC, e.g. a hyperlink
			j := i + 2
			for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
				j++
			}
			if j < len(s) && s[j] == 0x1b {
				j++
			}
			i = j
		case c == 0x1b:
			i++ // two-byte escape
		case c == '\n':
			endLine()
			out = append(out, c)
		default:
			if c != ' ' {
				if faint && ghost < 0 {
					ghost = len(out)
				} else if !faint {
					ghost = -1
				}
			}
			out = append(out, c)
		}
	}
	endLine()
	return string(out)
}

// sgrFaint applies one SGR parameter list to the faint flag. Colour
// arguments (38;5;n, 38;2;r;g;b) are skipped so their 2s are not read as
// faint; the colon form (38:2::r:g:b) is one field and needs no skipping.
func sgrFaint(params string, faint bool) bool {
	if params == "" {
		return false
	}
	ps := strings.Split(params, ";")
	for k := 0; k < len(ps); k++ {
		head, _, _ := strings.Cut(ps[k], ":")
		n, _ := strconv.Atoi(head)
		switch n {
		case 0, 22:
			faint = false
		case 2:
			faint = true
		case 38, 48, 58:
			if k+1 < len(ps) && ps[k+1] == "5" {
				k += 2
			} else if k+1 < len(ps) && ps[k+1] == "2" {
				k += 4
			}
		}
	}
	return faint
}
