package status

import (
	"regexp"
	"strconv"
	"strings"
)

// Menu is a picker on screen: numbered (Copilot/Claude approval boxes:
// "❯ 1. Yes", "  2. Yes, and …", "  3. No, …") or an unnumbered ❯ list
// (Claude's folder-trust check, agy's: "❯ No, exit" / "  Yes, I trust this
// folder"). Both are answered the same way, with Up/Down and Enter, so
// unnumbered options are numbered here too and Options always reads
// "N. label".
type Menu struct {
	Question string   // closest line above the first option ending in "?"
	Options  []string // "N. label", label cut to LabelMax runes
	Selected int      // 1-based option under the ❯ cursor
	// Numbered is false when the options were not numbered on screen, so a
	// caller can say so instead of quoting numbers the user cannot see.
	Numbered bool
}

// LabelMax caps an option label.
const LabelMax = 60

var optionRe = regexp.MustCompile(`^\s*([❯>])?\s*(\d)\.\s+(.+)$`)

// ParseMenu finds a menu in the bottom Window lines of text; see Set.Menu.
func ParseMenu(text string) (Menu, bool) { return Set{}.Menu(text) }

// Menu finds the bottom-most run of options numbered 1, 2, … with a cursor
// on one of them, skipping transcript lines so a numbered list the agent
// wrote is not taken for a prompt. When there is no numbered menu it looks
// for an unnumbered ❯ list instead (plainMenu).
func (s Set) Menu(text string) (Menu, bool) {
	lines := Tail(text, Window)
	var best Menu
	found := false
	for i := 0; i < len(lines); i++ {
		m, end, ok := s.menuAt(lines, i)
		if ok {
			best, found, i = m, true, end
		}
	}
	if found {
		return best, true
	}
	return s.plainMenu(tailRaw(text, 2*Window))
}

// menuAt reads a run of options starting at lines[i] ("1. …").
func (s Set) menuAt(lines []string, i int) (Menu, int, bool) {
	var m Menu
	j := i
	for ; j < len(lines); j++ {
		if matchAny(s.Transcript, lines[j]) {
			break
		}
		g := optionRe.FindStringSubmatch(clean(lines[j]))
		if g == nil || g[2] != strconv.Itoa(len(m.Options)+1) {
			break
		}
		if g[1] != "" {
			m.Selected = len(m.Options) + 1
		}
		m.Options = append(m.Options, g[2]+". "+Cut(strings.TrimSpace(g[3]), LabelMax))
	}
	if len(m.Options) < 2 || m.Selected == 0 {
		return Menu{}, i, false
	}
	for k := i - 1; k >= 0; k-- {
		if q := clean(lines[k]); strings.HasSuffix(q, "?") {
			m.Question = q
			break
		}
	}
	m.Numbered = true
	return m, j - 1, true
}

// Label is an option without its "N. " prefix.
func Label(opt string) string {
	if _, l, ok := strings.Cut(opt, ". "); ok {
		return l
	}
	return opt
}

// Cut shortens s to n runes, ending in "…" when cut.
func Cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return string(r[:n-1]) + "…"
}
