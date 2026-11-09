// Package status classifies an agent from the bottom of its pane.
// Patterns are data (built-in TOML + ~/.fleet/patterns.toml), one set per tool.
package status

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sturlabragason/fleet/internal/conf"
	"github.com/sturlabragason/fleet/internal/logx"
)

// Status of an agent.
type Status string

const (
	Working Status = "working"
	Waiting Status = "waiting"
	Error   Status = "error"
	Stuck   Status = "stuck"
	Idle    Status = "idle"
	Unknown Status = "unknown"
	Gone    Status = "gone"
	// Queued is a spawn held back by the working cap (DESIGN §7a): on the
	// board with no pane yet.
	Queued Status = "queued"
	// Starting is a queued agent claimed by the overview TUI and being launched.
	Starting Status = "starting"
	// Parked is an idle agent whose process was stopped to free memory
	// (DESIGN §7, ADR-44): on the board with no pane, keeping its session
	// id, so `fleet wake` resumes the same conversation in place.
	Parked Status = "parked"
)

// DefaultPatterns, per tool. Detect reads the last Window non-empty lines:
//
//  1. waiting: any match in the window wins. An approval box or question form
//     can sit above a footer that still says "Working", and its key hints are
//     the most reliable sign the tool is blocked on me. List the most
//     descriptive pattern first: the line it matches becomes waiting_for.
//     Lines matching `transcript` (the scrolling conversation) only count
//     when they are its last content line, so prompt text an agent merely
//     quoted earlier in its work doesn't make it look blocked.
//     Then a `pending` match (text pasted into the input box but not
//     submitted, e.g. Copilot's "[Paste #1 - 13 lines]") is waiting too, with
//     waiting_for "unsubmitted input: …".
//  2. otherwise the bottom-most line that matches error, working or idle
//     decides (within one line: error > working > idle). That is normally the
//     tool's footer, so text quoted in the transcript above it doesn't count.
//
// `input` matches a line of the prompt box; its first capture group is the
// text typed there (InputBox). Copilot draws the box as "┃ <text>" lines
// (or "❯ <text>" between rules, like Claude's "> <text>"), wrapped text
// continuing on indented rows. Fleet types report wakes only into an empty
// box. `placeholder` matches the hint an empty box shows instead (Claude's
// `Try "refactor <filepath>"`), which is not typed text.
//
// `scan = 'window'` is for tools that draw their state above the prompt box
// rather than in a footer under it (Claude, agy, vibe): working, then error,
// wins anywhere in the window, like waiting. Without it only the bottom-most
// line decides, which for those tools is a mode footer that never changes.
//
// Claude's empty box is "❯" plus a non-breaking space, which `\s` does not
// match, so its box patterns spell out \x{a0}.
//
// `error` must match failures only, never a warning the tool leaves on screen:
// vibe's "⚠ WARNING: You are in the home directory" sits there all session, so
// the ⚠ patterns are narrowed to ⚠ failed/error (as agy's already were).
// A banner that is permanently on screen would otherwise make the agent read
// as error for good, and stuck on its next screen (ADR-45). vibe's ⚠ failure
// form has never been captured (its offline retry backs off for minutes
// before giving up), so that half mirrors agy's shipped pattern and is
// untested until someone snaps one — ADR-42's accepted cost.
//
// Copilot CLI (newer UI): busy = "● Working · … esc esc interrupt" in the
// footer; idle = the mode footer ("Interactive · Allow All · / commands ·
// space hold to record") with no "Working". Picker hints like "esc to cancel"
// mean waiting, never working.
const DefaultPatterns = `
[fake]
input = ['^>\s?(.*)$']
waiting = ['\[y/n\]\s*$']
error = ['^✖ Error']
working = ['\(esc to interrupt\)', '^● Task:']
idle = ['^>\s*$', '^✔ Done']

[copilot]
transcript = ['^[^┃].*┃\s*$']
pending = ['^┃ \[Paste #\d+']
input = ['^┃(?: (.*))?$', '^❯(?: (.*))?$']
waiting = ['needs information\.', 'Do you want to', '\[y/n\]', 'Allow .* to run', '❯ 1\. Yes', '○ Asking user', '↑/↓ (to )?(select|navigate) · enter']
error = ['^✖', '(?i)^error:']
working = ['(?i)(esc (esc )?|ctrl\+c )(to )?interrupt', '(?i)^\s*([^·]{1,30} · )*\S? ?working · ', '[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]', '(?i)^\s*\S? ?thinking']
idle = ['^\s*[>❯]\s*$', '(?i)type @ to mention', '(?i)hold to record', '(?i)^\s*(← open sidebar · )?(interactive|autopilot|plan) · ']

[claude]
scan = 'window'
input = ['^[\s\x{a0}]*[>❯][\s\x{a0}](.*)$', '^[\s\x{a0}]*[>❯]()[\s\x{a0}]*$']
placeholder = ['^Try "']
waiting = ['Do you want to', 'Quick safety check', '(?i)this command requires approval', '❯ 1\. Yes', '\[y/n\]', '(?i)enter to confirm · esc to cancel']
error = ['(?i)^\s*●?\s*API Error', '(?i)^\s*(fatal|panic):']
working = ['(?i)esc to interrupt', '^\s*[✻✽✶✳✢] \w+…']
idle = ['^[\s\x{a0}]*[>❯][\s\x{a0}]*$', '\? for shortcuts', '^\s*[⏸⏵]+ \w+ mode on']

[agy]
scan = 'window'
input = ['^[\s\x{a0}]*>[\s\x{a0}](.*)$', '^[\s\x{a0}]*>()[\s\x{a0}]*$']
placeholder = ['(?i)^ask anything']
waiting = ['(?i)requesting permission for', '(?i)do you trust', 'Run this command\?', '\[y/n\]', '(?i)approve', '(?i)allow']
error = ['(?i)^\s*⚠ (failed|error)', '^Error ID: ']
working = ['(?i)esc to (interrupt|cancel)', '[\x{2800}-\x{28ff}]\s+\w+(…|\.\.\.)']
idle = ['^[\s\x{a0}]*>[\s\x{a0}]*$', '(?i)ctx [░▒▓█]+ +\d+%']

[vibe]
scan = 'window'
input = ['^[\s\x{a0}]*>[\s\x{a0}](.*)$', '^[\s\x{a0}]*>()[\s\x{a0}]*$']
placeholder = ['(?i)^ask anything']
waiting = ['(?i)permission for the ', '(?i)allow once', '\[y/n\]', '(?i)approve', '(?i)allow']
error = ['(?i)^\s*[⎣⎿]?\s*error: failed to', '(?i)^\s*⚠ (failed|error)']
working = ['(?i)esc(/ctrl\+c)? to (interrupt|cancel)', '[\x{2800}-\x{28ff}]\s+\w+(…|\.\.\.)']
idle = ['^[\s\x{a0}]*>[\s\x{a0}]*$', '(?i)/\d+k tokens \(']
`

// Set holds compiled patterns for one tool.
type Set struct {
	Waiting, Error, Working, Idle []*regexp.Regexp
	Transcript                    []*regexp.Regexp // lines of the scrolling conversation
	Pending                       []*regexp.Regexp // unsubmitted input in the prompt box
	Input                         []*regexp.Regexp // a prompt-box line; group 1 = its text
	Placeholder                   []*regexp.Regexp // the hint an empty prompt box shows
	// Scan "window" lets working, then error, win anywhere in the window,
	// for tools that draw them above the prompt box instead of in a footer.
	Scan string
}

// Sets maps pattern-set name to Set.
type Sets map[string]Set

// Load parses built-ins then overrides from <home>/patterns.toml.
func Load(home string) Sets {
	log := logx.For("status")
	out := Sets{}
	def, _ := conf.Parse(DefaultPatterns)
	user, err := conf.ParseFile(filepath.Join(home, "patterns.toml"))
	if err != nil {
		log.Error("patterns.toml", "err", err)
	}
	for _, d := range []conf.Doc{def, user} {
		for name, sec := range d {
			if name == "" {
				continue
			}
			s := out[name]
			for k, dst := range map[string]*[]*regexp.Regexp{"waiting": &s.Waiting, "error": &s.Error, "working": &s.Working, "idle": &s.Idle, "transcript": &s.Transcript, "pending": &s.Pending, "input": &s.Input, "placeholder": &s.Placeholder} {
				if l, ok := sec[k].([]string); ok {
					*dst = compile(name, l)
				}
			}
			if v, ok := sec["scan"].(string); ok {
				s.Scan = v
			}
			out[name] = s
		}
	}
	return out
}

func compile(set string, l []string) []*regexp.Regexp {
	var rs []*regexp.Regexp
	for _, p := range l {
		r, err := regexp.Compile(p)
		if err != nil {
			logx.For("status").Error("bad pattern", "set", set, "pattern", p, "err", err)
			continue
		}
		rs = append(rs, r)
	}
	return rs
}

// Tail returns the last n non-empty lines of text.
func Tail(text string, n int) []string {
	lines := strings.Split(strings.TrimRight(text, "\n "), "\n")
	var out []string
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			out = append([]string{strings.TrimRight(lines[i], " ")}, out...)
		}
	}
	return out
}

// Window is how many non-empty bottom lines Detect reads: enough for a
// Copilot question form or approval box plus the footer under it.
const Window = 14

// deadPane is tmux's remain-on-exit banner; it means exited whatever the tool.
var deadPane = regexp.MustCompile(`^Pane is dead \(`)

// Detect classifies pane text. ok=false when nothing matched.
func (s Set) Detect(text string) (Status, bool) {
	st, _, ok := s.Match(text)
	return st, ok
}

// Match is Detect plus the line that explains the status: for waiting, the
// prompt (e.g. "Do you want to run this command?"), else the matching line.
func (s Set) Match(text string) (Status, string, bool) {
	lines := Tail(text, Window)
	for _, l := range lines {
		if deadPane.MatchString(l) {
			return Error, strings.TrimSpace(l), true
		}
	}
	last := -1 // last transcript line with content
	skip := make([]bool, len(lines))
	for i, l := range lines {
		if matchAny(s.Transcript, l) {
			skip[i] = true
			if clean(l) != "" {
				last = i
			}
		}
	}
	if last >= 0 {
		skip[last] = false
	}
	for _, r := range s.Waiting {
		for i, l := range lines {
			if !skip[i] && r.MatchString(l) {
				return Waiting, describe(lines, i, s.Waiting), true
			}
		}
	}
	if l, ok := s.pending(lines, skip); ok {
		return Waiting, PendingPrefix + l, true
	}
	if s.Scan == "window" {
		// Claude, agy and vibe put the spinner and the error above the
		// prompt box; the footer under it says the same thing idle or busy.
		for _, c := range []struct {
			st Status
			rs []*regexp.Regexp
		}{{Working, s.Working}, {Error, s.Error}} {
			for i := len(lines) - 1; i >= 0; i-- {
				if !skip[i] && matchAny(c.rs, lines[i]) {
					return c.st, clean(lines[i]), true
				}
			}
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		for _, c := range []struct {
			st Status
			rs []*regexp.Regexp
		}{{Error, s.Error}, {Working, s.Working}, {Idle, s.Idle}} {
			for _, r := range c.rs {
				if r.MatchString(l) {
					return c.st, clean(l), true
				}
			}
		}
	}
	return Unknown, "", false
}

// PendingPrefix starts waiting_for when the only reason is unsubmitted input.
const PendingPrefix = "unsubmitted input: "

// Unsubmitted reports unsubmitted input in the prompt box and its cleaned line.
func (s Set) Unsubmitted(text string) (string, bool) {
	lines := Tail(text, Window)
	skip := make([]bool, len(lines))
	for i, l := range lines {
		skip[i] = matchAny(s.Transcript, l)
	}
	return s.pending(lines, skip)
}

// InputBox returns the text in the prompt box: the bottom-most run of
// lines matching `input` plus the indented rows a wrapped entry continues
// on below it (up to a rule), their text joined by spaces. A box showing
// only its `placeholder` hint reads as empty. ok=false when the set has no
// input pattern or no box is on screen.
func (s Set) InputBox(text string) (string, bool) {
	if len(s.Input) == 0 {
		return "", false
	}
	lines := Tail(text, Window)
	top, bot := -1, -1
	for i := len(lines) - 1; i >= 0; i-- {
		if _, ok := inputLine(s.Input, lines[i]); ok {
			if bot < 0 {
				bot = i
			}
			top = i
		} else if bot >= 0 {
			break
		}
	}
	if bot < 0 {
		return "", false
	}
	var parts []string
	for i := top; i <= bot; i++ {
		t, _ := inputLine(s.Input, lines[i])
		parts = append(parts, t)
	}
	for i := bot + 1; i < len(lines); i++ {
		l := lines[i]
		if !strings.HasPrefix(l, " ") || strings.Trim(l, boxChars+" ") == "" {
			break
		}
		parts = append(parts, strings.TrimSpace(l))
	}
	box := strings.TrimSpace(strings.Join(parts, " "))
	if matchAny(s.Placeholder, box) {
		return "", true
	}
	return box, true
}

func inputLine(rs []*regexp.Regexp, l string) (string, bool) {
	for _, r := range rs {
		if m := r.FindStringSubmatch(l); m != nil {
			if len(m) > 1 {
				return strings.TrimSpace(m[1]), true
			}
			return "", true
		}
	}
	return "", false
}

func (s Set) pending(lines []string, skip []bool) (string, bool) {
	for i, l := range lines {
		if !skip[i] && matchAny(s.Pending, l) {
			return clean(l), true
		}
	}
	return "", false
}

func matchAny(rs []*regexp.Regexp, l string) bool {
	for _, r := range rs {
		if r.MatchString(l) {
			return true
		}
	}
	return false
}

// describe returns lines[i] cleaned of box borders, plus the next line when
// that is plain text (the question under "Copilot needs information.").
func describe(lines []string, i int, waiting []*regexp.Regexp) string {
	d := clean(lines[i])
	if i+1 >= len(lines) {
		return d
	}
	next := clean(lines[i+1])
	if next == "" || strings.Trim(next, "─━-╌ ") == "" || strings.HasSuffix(d, "?") {
		return d
	}
	if matchAny(waiting, lines[i+1]) {
		return d
	}
	return d + " " + next
}

// clean trims whitespace and box-drawing borders from a pane line.
func clean(l string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "│┃║"))
}
