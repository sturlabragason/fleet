package fleet

import (
	"fmt"
	"slices"
	"strings"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/status"
)

// allowedKeys are the tmux key names fleet_send may press. Nothing that
// signals or edits outside the input box (no C-c, no C-d).
var allowedKeys = map[string]bool{
	"Enter": true, "Escape": true, "Up": true, "Down": true, "Left": true, "Right": true,
	"Tab": true, "BTab": true, "Space": true, "BSpace": true, "y": true, "n": true,
	"1": true, "2": true, "3": true, "4": true, "5": true, "6": true, "7": true, "8": true, "9": true,
}

// CheckKeys returns an error naming the first key not on the allowlist.
func CheckKeys(keys []string) error {
	for _, k := range keys {
		if !allowedKeys[k] {
			return fmt.Errorf("key %q not allowed (use Enter, Escape, Up, Down, Left, Right, Tab, BTab, Space, BSpace, 1-9, y, n)", k)
		}
	}
	return nil
}

// Prompting reports what the agent waits on right now, from a fresh capture
// ("" = not waiting, or only on its own unsubmitted input, which keys can
// submit without answering anything). A numbered menu on screen counts.
func (f *Fleet) Prompting(name string) (string, error) {
	if f.isACP(name) {
		q, err := f.acpQuestion(name)
		if err != nil || q == nil {
			return "", err
		}
		return strings.TrimSpace(q.Title + " " + strings.Join(acpMenu(q).Options, " | ")), nil
	}
	p, err := f.Pane(name)
	if err != nil {
		return "", err
	}
	text, err := f.T.Capture(p, 0)
	if err != nil {
		return "", err
	}
	set := f.setOf(name)
	st, line, _ := set.Match(text)
	if st == status.Waiting && !strings.HasPrefix(line, status.PendingPrefix) {
		if line == "" {
			line = "prompt"
		}
		return line, nil
	}
	// A numbered picker counts even when the tool's patterns miss it.
	if m, ok := set.Menu(text); ok {
		return strings.TrimSpace(m.Question + " " + strings.Join(m.Options, " | ")), nil
	}
	return "", nil
}

// WaitingError is the refusal for sending to an agent on a prompt without
// confirmation.
func WaitingError(prompt string) error {
	return fmt.Errorf("agent is waiting on a prompt: text or keys can answer it; ask the user, then call again with confirmed=true (or use fleet_answer). Prompt: %s", status.Cut(prompt, 120))
}

func waitingNote(prompt string, err error) string {
	if err != nil {
		return err.Error()
	}
	return WaitingError(prompt).Error()
}

// SendKeys pastes text (without Enter) when given, then presses keys.
// Keys must pass CheckKeys.
func (f *Fleet) SendKeys(name, text string, keys []string) error {
	if err := CheckKeys(keys); err != nil {
		return err
	}
	if f.isACP(name) {
		return f.acpKeys(name, text, keys)
	}
	p, err := f.Pane(name)
	if err != nil {
		return err
	}
	if text != "" {
		if err := f.T.Paste(p, text, false); err != nil {
			return err
		}
	}
	logx.For("fleet").Info("send keys", "agent", name, "bytes", len(text), "keys", keys)
	if len(keys) == 0 {
		return nil
	}
	if err := f.T.SendKeys(p, keys...); err != nil {
		return err
	}
	if text != "" && slices.Contains(keys, "Enter") {
		f.notePrompt(name)
	}
	return nil
}

// Menu returns the numbered prompt on the agent's screen, if any.
func (f *Fleet) Menu(name string) (status.Menu, bool, error) {
	if f.isACP(name) {
		q, err := f.acpQuestion(name)
		if err != nil || q == nil {
			return status.Menu{}, false, err
		}
		return acpMenu(q), true, nil
	}
	p, err := f.Pane(name)
	if err != nil {
		return status.Menu{}, false, err
	}
	text, err := f.T.Capture(p, 0)
	if err != nil {
		return status.Menu{}, false, err
	}
	m, ok := f.setOf(name).Menu(text)
	return m, ok, nil
}

// Choice is what Choose or Answer picked and the prompt it answered.
type Choice struct {
	Label    string // "N. label"; "" when y/n was typed with no menu on screen
	Question string // the menu's question, or the waiting line without a menu
}

// Choose picks option (1-based) in the numbered prompt on screen: Up/Down
// from the current ❯ item, then Enter. A non-empty expect must be a
// case-insensitive substring of the question or of the option's label, so a
// prompt that changed since the user said yes is not answered.
func (f *Fleet) Choose(name string, option int, expect string) (Choice, error) {
	return f.choose(name, option, expect, "no prompt with options on screen")
}

// ChooseLabel picks the one option whose label contains label (case
// insensitive), which is how an unnumbered ❯ menu is answered without
// quoting numbers that are not on screen. expect is checked as for Choose.
func (f *Fleet) ChooseLabel(name, label, expect string) (Choice, error) {
	m, ok, err := f.Menu(name)
	if err != nil {
		return Choice{}, err
	}
	if !ok {
		return Choice{}, fmt.Errorf("no prompt with options on screen")
	}
	want := strings.ToLower(strings.TrimSpace(label))
	hit := 0
	for i, o := range m.Options {
		if strings.Contains(strings.ToLower(status.Label(o)), want) {
			if hit != 0 {
				return Choice{}, fmt.Errorf("%q matches more than one option: %s", label, strings.Join(m.Options, " | "))
			}
			hit = i + 1
		}
	}
	if hit == 0 {
		return Choice{}, fmt.Errorf("no option matches %q (have %s)", label, strings.Join(m.Options, " | "))
	}
	return f.Choose(name, hit, expect)
}

// choose re-reads the menu right before pressing keys; gone is the error
// when there is none.
func (f *Fleet) choose(name string, option int, expect, gone string) (Choice, error) {
	if f.isACP(name) {
		return f.acpChoose(name, option, expect, gone)
	}
	p, err := f.Pane(name)
	if err != nil {
		return Choice{}, err
	}
	m, ok, err := f.Menu(name)
	if err != nil {
		return Choice{}, err
	}
	if !ok {
		return Choice{}, fmt.Errorf("%s", gone)
	}
	if option < 1 || option > len(m.Options) {
		return Choice{}, fmt.Errorf("option %d not on screen (have 1-%d)", option, len(m.Options))
	}
	c := Choice{Label: m.Options[option-1], Question: m.Question}
	if !expected(expect, c.Question, status.Label(c.Label)) {
		now := c.Question
		if now == "" {
			now = strings.Join(m.Options, " | ")
		}
		return Choice{}, fmt.Errorf("prompt changed: now '%s'", now)
	}
	if err := f.T.SendKeys(p, MenuKeys(m.Selected, option)...); err != nil {
		return Choice{}, err
	}
	logx.For("fleet").Info("chose", "agent", name, "option", c.Label, "question", c.Question)
	return c, nil
}

// expected reports whether expect ("" = anything) is a case-insensitive
// substring of one of in.
func expected(expect string, in ...string) bool {
	e := strings.ToLower(strings.TrimSpace(expect))
	if e == "" {
		return true
	}
	for _, s := range in {
		if s != "" && strings.Contains(strings.ToLower(s), e) {
			return true
		}
	}
	return false
}

// MenuKeys moves the cursor from selected to option and presses Enter.
func MenuKeys(selected, option int) []string {
	var keys []string
	for i := selected; i < option; i++ {
		keys = append(keys, "Down")
	}
	for i := selected; i > option; i-- {
		keys = append(keys, "Up")
	}
	return append(keys, "Enter")
}

// Answer answers y or n. With a prompt on screen, y picks the first option
// starting with "Yes" (else option 1) and n the first starting with "No" —
// pickers do not always put the yes first, as Claude's folder-trust dialog
// ("❯ No, exit" / "Yes, I trust this folder") shows. Without one it types
// y/n and Enter as before (Label ""); expect is then checked against the
// waiting line.
func (f *Fleet) Answer(name, yn, expect string) (Choice, error) {
	if yn != "y" && yn != "n" {
		return Choice{}, fmt.Errorf(`answer must be "y" or "n"`)
	}
	if f.isACP(name) {
		return f.acpAnswer(name, yn, expect)
	}
	m, ok, err := f.Menu(name)
	if err != nil {
		return Choice{}, err
	}
	if !ok {
		w, err := f.Prompting(name)
		if err != nil {
			return Choice{}, err
		}
		if !expected(expect, w) {
			return Choice{}, fmt.Errorf("prompt changed: now '%s'", w)
		}
		return Choice{Question: w}, f.Send(name, yn)
	}
	target := first(m.Options, "Yes")
	if yn == "n" {
		if target = first(m.Options, "No"); target == 0 {
			return Choice{}, fmt.Errorf("no option starting with \"No\" in %s; use option", strings.Join(m.Options, " | "))
		}
	} else if target == 0 {
		target = 1
	}
	return f.choose(name, target, expect, "prompt no longer on screen")
}

// first is the 1-based index of the first option whose label starts with
// word (case-insensitive), or 0.
func first(options []string, word string) int {
	for i, o := range options {
		if strings.HasPrefix(strings.ToLower(status.Label(o)), strings.ToLower(word)) {
			return i + 1
		}
	}
	return 0
}
