package status

import (
	"regexp"
	"strconv"
	"strings"
)

// plainMax caps an unnumbered option label; longer lines are prose, not a
// menu entry.
const plainMax = 80

// plainQuestionUp is how far above the options plainMenu looks for the
// question the menu answers. It is the read window: Claude puts a paragraph,
// a list of paths and a security note between its question and the options.
const plainQuestionUp = Window

// markers are the cursor glyphs a tool draws beside the selected option.
// "❯" and "›" belong to pickers; a plain ">" also starts a quoted shell
// command in an agent's own output, so a ">" block only counts as a menu
// when the screen says how to answer it (weakHint).
const markers = "❯›>"

// weak are the markers that need corroboration.
const weak = ">"

// hintRe matches the key hint a picker prints under its options ("↑/↓
// Navigate · enter Confirm", "Enter to confirm · Esc to cancel").
var hintRe = regexp.MustCompile(`(?i)[↑↓]|\benter\b.{0,20}\b(confirm|select|choose)\b|\besc\b.{0,20}\bcancel\b|\bnavigate\b|\buse arrow`)

// tailRaw is Tail with the blank lines kept, so a run of options can be
// told from two unrelated blocks that happen to be indented alike.
func tailRaw(text string, n int) []string {
	lines := strings.Split(strings.TrimRight(text, "\n "), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// plainMenu finds an unnumbered picker: one line marked with a cursor glyph
// and at least one sibling whose label starts in the same column (Claude's
// folder-trust check, agy's "Do you trust…"). Labels must read like options,
// not prose: short, with a letter or digit in them, and never a box rule. A
// prompt box ("❯ what I typed") has no sibling at its own label column, so it
// is not a menu.
func (s Set) plainMenu(lines []string) (Menu, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		if matchAny(s.Transcript, lines[i]) {
			continue
		}
		col, marked, label, ok := optionLine(lines[i])
		if !ok || !marked {
			continue
		}
		top, bot := i, i
		for j := i - 1; j >= 0 && s.sibling(lines[j], col); j-- {
			top = j
		}
		for j := i + 1; j < len(lines) && s.sibling(lines[j], col); j++ {
			bot = j
		}
		if bot-top < 1 {
			continue
		}
		var m Menu
		for j := top; j <= bot; j++ {
			l := label
			if j != i {
				_, _, l, _ = optionLine(lines[j])
			}
			m.Options = append(m.Options, strconv.Itoa(len(m.Options)+1)+". "+Cut(l, LabelMax))
			if j == i {
				m.Selected = len(m.Options)
			}
		}
		m.Question = plainQuestion(lines, top)
		if strings.ContainsAny(string([]rune(strings.TrimSpace(lines[i]))[0:1]), weak) && !s.hinted(lines, bot) {
			// Just a quoted command and a short line after it.
			continue
		}
		return m, true
	}
	return Menu{}, false
}

// sibling reports whether l is another option of a menu whose labels start at
// column col: same column, no cursor of its own, and option-shaped.
func (s Set) sibling(l string, col int) bool {
	if matchAny(s.Transcript, l) {
		return false
	}
	c, marked, _, ok := optionLine(l)
	return ok && !marked && c == col
}

// optionLine splits a pane line into the column its label starts at, whether
// a cursor marks it, and the label. ok=false for blank lines, box rules and
// anything too long to be an option.
func optionLine(l string) (col int, marked bool, label string, ok bool) {
	r := []rune(strings.TrimRight(l, " \t"))
	i := 0
	for i < len(r) && (r[i] == ' ' || r[i] == '\t') {
		i++
	}
	if i >= len(r) {
		return 0, false, "", false
	}
	if strings.ContainsRune(markers, r[i]) && i+2 < len(r) && r[i+1] == ' ' {
		j := i + 1
		for j < len(r) && r[j] == ' ' {
			j++
		}
		if j >= len(r) {
			return 0, false, "", false
		}
		i, marked = j, true
	}
	label = strings.TrimSpace(string(r[i:]))
	if len([]rune(label)) > plainMax || !hasWord(label) {
		return 0, false, "", false
	}
	return i, marked, label, true
}

// hasWord reports whether s holds a letter or digit, so box rules ("────"),
// separators and spinner frames are never options.
func hasWord(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return true
		}
	}
	return false
}

// plainQuestion is the nearest line above the options that ends in "?", else
// the nearest that contains one (Claude wraps its trust question mid-line).
func plainQuestion(lines []string, top int) string {
	var loose string
	for k := top - 1; k >= 0 && k >= top-plainQuestionUp; k-- {
		q := clean(lines[k])
		if strings.HasSuffix(q, "?") {
			return q
		}
		if loose == "" && strings.Contains(q, "?") {
			loose = q
		}
	}
	return Cut(loose, 2*LabelMax)
}

// hinted reports whether the screen tells the user how to work the picker,
// in the few lines under the options where a picker prints it. A hint the
// agent quoted in its own prose does not count, which is why transcript
// lines are skipped: on a screen full of quoted output, any ">" block would
// otherwise read as a menu.
func (s Set) hinted(lines []string, bot int) bool {
	for j := bot + 1; j < len(lines) && j <= bot+hintWithin; j++ {
		if matchAny(s.Transcript, lines[j]) {
			continue
		}
		if hintRe.MatchString(lines[j]) {
			return true
		}
	}
	return false
}

// hintWithin is how far under the last option the key hint may sit.
const hintWithin = 4
