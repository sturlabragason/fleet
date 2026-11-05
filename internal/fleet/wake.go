package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/supervisor"
)

// Report wakes (DESIGN §6.7, ADR-36). A report is always on disk and on the
// status-line badge; typing a line into the admiral pane is only a wake-up
// and happens only when fleet can see the input box and it is empty. Enter
// is pressed only when the box then holds exactly the wake line.

// AdmiralView is what fleet reads from the admiral pane before typing.
type AdmiralView struct {
	Pane  string
	Idle  bool
	Box   bool   // the input box was found on screen
	Input string // text in the input box ("" = empty)
}

// Quiet is how long after the user's last key press in the fleet session
// a wake may be typed.
var Quiet = 3 * time.Second

// GuardWait is how long the Enter guard waits for the pasted line to show.
var GuardWait = 1500 * time.Millisecond

// BadgeOption is the tmux user option the status line shows (`fleet up`).
const BadgeOption = "@fleet_reports"

// wakeSupervisor queues a report for the admiral pane and tries to deliver
// it now. Queued reports are coalesced into one line (WakeLine).
// id is the report's mail id: an entry is typed only while it is unread.
func (f *Fleet) wakeSupervisor(id, from, state, line string) {
	p := f.wakePath()
	os.MkdirAll(filepath.Dir(p), 0o755)
	clean := func(s string) string { return strings.NewReplacer("\t", " ", "\n", " ").Replace(s) }
	entry := clean(id) + "\t" + clean(from) + "\t" + clean(state) + "\t" + clean(line) + "\n"
	if fh, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fh.WriteString(entry)
		fh.Close()
	}
	f.DeliverWake()
}

func (f *Fleet) wakePath() string {
	return filepath.Join(f.Home, "mail", mail.Supervisor, "wake-pending")
}

type wakeEntry struct{ raw, from, state, line string }

// parseWake reads the queue, keeping entries whose report is still unread.
// Lines in the pre-ADR-36 format (one bare line) are dropped.
func parseWake(b []byte, unread []mail.Msg) []wakeEntry {
	ids := map[string]bool{}
	for _, m := range unread {
		ids[m.ID] = true
	}
	var out []wakeEntry
	for _, l := range strings.Split(string(b), "\n") {
		if p := strings.SplitN(l, "\t", 4); len(p) == 4 && ids[p[0]] {
			out = append(out, wakeEntry{l, p[1], p[2], p[3]})
		}
	}
	return out
}

// takeWake moves the queue aside under a name of its own (so a report
// appended meanwhile starts a new queue, never lost) and returns its
// still-unread entries, judged after the move so a report written just
// before it counts.
func (f *Fleet) takeWake() []wakeEntry {
	p := f.wakePath()
	tmp, err := os.CreateTemp(filepath.Dir(p), "wake-pending.take-*")
	if err != nil {
		return nil
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if os.Rename(p, tmp.Name()) != nil {
		return nil // another process took it
	}
	b, _ := os.ReadFile(tmp.Name())
	return parseWake(b, f.Mail().Peek(mail.Supervisor))
}

func (f *Fleet) requeueWake(es []wakeEntry) {
	if len(es) == 0 {
		return
	}
	fh, err := os.OpenFile(f.wakePath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer fh.Close()
	for _, e := range es {
		fh.WriteString(e.raw + "\n")
	}
}

// WakeLine is the one line typed for a batch of queued reports: the report
// line itself for one, else "[fleet] N new reports: a (done), b (blocked)…".
func WakeLine(es []wakeEntry) string {
	if len(es) == 1 {
		return es[0].line
	}
	var names []string
	seen := map[string]int{}
	for _, e := range es {
		n := e.from + " (" + e.state + ")"
		if i, ok := seen[e.from]; ok {
			names[i] = n
			continue
		}
		seen[e.from] = len(names)
		names = append(names, n)
	}
	return fmt.Sprintf("[fleet] %d new reports: %s. Full text: call fleet_reports.", len(es), strings.Join(names, ", "))
}

// DeliverWake refreshes the badge and types the queued wake into the
// admiral pane if, and only if, the pane is idle, its input box is on
// screen and empty, and no key was pressed in the session for Quiet. The
// queue is dropped once the admiral has read its reports or was already
// woken for this unread batch.
func (f *Fleet) DeliverWake() {
	unread := f.Mail().Peek(mail.Supervisor)
	f.syncBadge(unread)
	if _, err := os.Stat(f.wakePath()); err != nil {
		return
	}
	if len(unread) == 0 {
		f.requeueWake(f.takeWake()) // drops what was read, keeps a report that just landed
		return
	}
	v := f.supPane()
	if v.Pane == "" || !v.Idle || !v.Box || v.Input != "" || !f.quiet() {
		return
	}
	es := f.takeWake()
	if len(es) == 0 {
		return
	}
	_, ok, err := f.Mail().ClaimNudge(mail.Supervisor)
	if err != nil {
		f.requeueWake(es)
		return
	}
	if !ok {
		return // already woken for this unread batch
	}
	line := WakeLine(es)
	if err := f.typeText(v.Pane, line); err != nil {
		logx.For("mail").Warn("admiral wake failed", "err", err)
		f.requeueWake(es)
		f.Mail().UnclaimNudge(mail.Supervisor)
		return
	}
	f.guardEnter(v.Pane, line)
}

// guardEnter presses Enter only if the box holds exactly line. Otherwise it
// takes back what it typed (the box was seen empty just before) and leaves
// the user's characters: line plus text typed after it is deleted and that
// text typed again; a box ending in line, or showing only its start, loses
// len(line) characters. The report stays on the badge.
func (f *Fleet) guardEnter(pane, line string) {
	log := logx.For("mail")
	want := squash(line)
	var raw, got string
	end := time.Now().Add(GuardWait)
	for {
		v := f.supPane()
		raw = v.Input
		got = squash(raw)
		if v.Pane == pane && v.Box && got == want {
			if err := f.pressKeys(pane, "Enter"); err != nil {
				log.Warn("admiral wake failed", "err", err)
				return
			}
			log.Info("woke admiral", "line", line)
			return
		}
		if time.Now().After(end) || !strings.HasPrefix(want, got) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	n := len([]rune(line))
	how := "left as is"
	switch {
	case strings.HasPrefix(got, want):
		tail := after(raw, want)
		f.pressKeys(pane, backspaces(n+len([]rune(tail)))...)
		if err := f.pressKeys(pane, "-l", "--", tail); err != nil {
			log.Warn("admiral wake: could not retype user text", "err", err)
		}
		how = "removed, user text after it kept"
	case strings.HasSuffix(got, want), got != "" && strings.HasPrefix(want, got):
		f.pressKeys(pane, backspaces(n)...)
		how = "removed"
	}
	log.Warn("admiral wake not submitted: input box did not hold exactly the wake line", "wake", how)
}

// after returns what follows the characters of want (whitespace ignored)
// at the start of raw.
func after(raw, want string) string {
	w := []rune(want)
	i := 0
	for j, r := range raw {
		if i == len(w) {
			return raw[j:]
		}
		if unicode.IsSpace(r) {
			continue
		}
		i++
	}
	return ""
}

func backspaces(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = "BSpace"
	}
	return keys
}

// squash drops all whitespace, so a line the input box wraps still matches.
func squash(s string) string {
	return strings.Join(strings.Fields(s), "")
}

func (f *Fleet) typeText(pane, text string) error {
	if f.TypeText != nil {
		return f.TypeText(pane, text)
	}
	return f.T.Paste(pane, text, false)
}

func (f *Fleet) pressKeys(pane string, keys ...string) error {
	if f.PressKeys != nil {
		return f.PressKeys(pane, keys...)
	}
	return f.T.SendKeys(pane, keys...)
}

// quiet reports whether no client of the fleet session pressed a key
// within Quiet (tmux client_activity, whole seconds).
func (f *Fleet) quiet() bool {
	out, err := f.T.Run("list-clients", "-F", "#{client_activity}")
	if err != nil {
		return true // no server or no client: nobody is typing
	}
	for _, l := range strings.Fields(out) {
		if s, err := strconv.ParseInt(l, 10, 64); err == nil && time.Since(time.Unix(s, 0)) < Quiet {
			return false
		}
	}
	return true
}

// Badge is the status-line text for the admiral's unread reports, "" for none.
func Badge(ms []mail.Msg) string {
	var names []string
	seen := map[string]bool{}
	n := 0
	for _, m := range ms {
		if m.Kind != "report" {
			continue
		}
		n++
		if !seen[m.From] {
			seen[m.From] = true
			names = append(names, m.From)
		}
	}
	switch {
	case n == 0:
		return ""
	case n == 1:
		return "1 report: " + names[0]
	}
	list := strings.Join(names, ", ")
	if r := []rune(list); len(r) > 32 {
		list = string(r[:31]) + "…"
	}
	return fmt.Sprintf("%d reports: %s", n, list)
}

// syncBadge sets the tmux badge option to the admiral's unread reports.
func (f *Fleet) syncBadge(unread []mail.Msg) {
	want := Badge(unread)
	have, _ := f.T.Run("show-options", "-gqv", BadgeOption)
	if strings.TrimSpace(have) == want {
		return
	}
	if want == "" {
		f.T.Run("set-option", "-gu", BadgeOption)
	} else {
		f.T.Run("set-option", "-g", BadgeOption, want)
	}
	f.T.Run("refresh-client", "-S")
}

// supPane finds the @role=sup pane in fleet:main and reads it with the
// admiral profile's status patterns (default copilot).
func (f *Fleet) supPane() AdmiralView {
	if f.SupPane != nil {
		return f.SupPane()
	}
	pane, err := supervisor.FindPane(f.T, Session+":main")
	if err != nil {
		return AdmiralView{}
	}
	prof := supervisor.Current(f.Home, f.T, Session+":main").Profile
	if prof == "" {
		prof = "copilot"
	}
	set := prof
	if p, ok := f.Profiles[prof]; ok && p.Patterns != "" {
		set = p.Patterns
	}
	text, err := f.T.Capture(pane, 0)
	if err != nil {
		return AdmiralView{Pane: pane}
	}
	ps := f.Patterns[set]
	st, ok := ps.Detect(text)
	in, box := ps.InputBox(text)
	return AdmiralView{Pane: pane, Idle: ok && st == status.Idle, Box: box, Input: in}
}
