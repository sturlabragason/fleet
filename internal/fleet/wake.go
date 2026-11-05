package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
	if fh, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fh.WriteString(wakeEntry{id: id, from: from, state: state, line: line}.raw() + "\n")
		fh.Close()
	}
	f.DeliverWake()
}

func (f *Fleet) wakePath() string {
	return filepath.Join(f.Home, "mail", mail.Supervisor, "wake-pending")
}

// wakeEntry is one queued report: its mail id, who sent it, the state, how
// many times fleet has typed it without it being submitted, and the line.
type wakeEntry struct {
	id, from, state, line string
	tries                 int
}

// raw is the queue line: id, from, state, tries, line, tab-separated.
func (e wakeEntry) raw() string {
	clean := func(s string) string { return strings.NewReplacer("\t", " ", "\n", " ").Replace(s) }
	return strings.Join([]string{clean(e.id), clean(e.from), clean(e.state), strconv.Itoa(e.tries), clean(e.line)}, "\t")
}

// parseWake reads the queue, keeping entries whose report is still unread.
// Lines in the pre-ADR-36 format (one bare line) are dropped; a line in the
// pre-ADR-51 format (no try count) is read as never tried.
func parseWake(b []byte, unread []mail.Msg) []wakeEntry {
	ids := map[string]bool{}
	for _, m := range unread {
		ids[m.ID] = true
	}
	var out []wakeEntry
	for _, l := range strings.Split(string(b), "\n") {
		p := strings.SplitN(l, "\t", 5)
		if len(p) < 4 || !ids[p[0]] {
			continue
		}
		e := wakeEntry{id: p[0], from: p[1], state: p[2], line: p[3]}
		if len(p) == 5 {
			e.tries, _ = strconv.Atoi(p[3])
			e.line = p[4]
		}
		out = append(out, e)
	}
	return out
}

// newestID is the highest message id in a batch: what the nudge mark moves
// to once the batch has been announced.
func newestID(es []wakeEntry) string {
	id := ""
	for _, e := range es {
		if e.id > id {
			id = e.id
		}
	}
	return id
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

// requeueWake puts entries back at the end of the queue. A failure here is
// the one way a report can be lost, so it is logged as an error.
func (f *Fleet) requeueWake(es []wakeEntry) error {
	if len(es) == 0 {
		return nil
	}
	fh, err := os.OpenFile(f.wakePath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		defer fh.Close()
		for _, e := range es {
			if _, werr := fh.WriteString(e.raw() + "\n"); werr != nil {
				err = werr
				break
			}
		}
	}
	if err != nil {
		logx.For("mail").Error("admiral wake lost: could not put it back on the queue",
			"err", err, "queued", len(es), "line", WakeLine(es))
	}
	return err
}

// lockWake takes the cross-process claim on typing into the admiral pane.
// The queue file keeps two processes from typing the same report, but not
// from typing two different ones into the same box at the same time: the
// reporting agent's own process and the watcher poll a second apart. false
// means somebody else holds it and this process types nothing.
func (f *Fleet) lockWake() (release func(), ok bool) {
	p := f.wakePath() + ".lock"
	os.MkdirAll(filepath.Dir(p), 0o755)
	lf, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return func() {}, false
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lf.Close()
		return func() {}, false
	}
	return func() {
		syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		lf.Close()
	}, true
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

// WakeMaxTries is how often fleet types a wake line that is never
// submitted before it gives up on typing it and leaves the badge to carry
// the report. A box fleet cannot read back would otherwise be typed into
// on every poll for as long as the report stays unread.
const WakeMaxTries = 3

// DeliverWake refreshes the badge and types the queued wake into the
// admiral pane if, and only if, the pane is idle, its input box is on
// screen and empty, no key was pressed in the session for Quiet, and no
// other process is typing into it. The queue is dropped only once the
// admiral has read its reports; anything else puts it back, so a wake that
// could not be typed is retried on the next poll (the watcher polls,
// DESIGN §7c) instead of being lost (ADR-51).
func (f *Fleet) DeliverWake() {
	unread := f.Mail().Peek(mail.Supervisor)
	f.syncBadge(unread)
	b, err := os.ReadFile(f.wakePath())
	if err != nil {
		return
	}
	es := parseWake(b, unread)
	if len(es) == 0 {
		f.requeueWake(f.takeWake()) // compacts: entries whose report was read go
		return
	}
	// Already announced: the admiral was told about every queued report and
	// has not read them yet. Leave the queue alone -- no rename, no log --
	// so the next new report coalesces these into its line.
	if newestID(es) <= f.Mail().NudgeMark(mail.Supervisor) {
		return
	}
	if !f.canType(f.supPane()) {
		f.stalledWake("admiral wake waiting for an idle, empty admiral pane", "queued", len(es))
		return
	}
	release, ok := f.lockWake()
	if !ok {
		return // another process is typing into the pane
	}
	defer release()
	// The pane is read again under the lock: whoever held it may have just
	// typed into the box, or taken these very entries.
	v := f.supPane()
	if !f.canType(v) {
		return
	}
	es = f.takeWake()
	if len(es) == 0 {
		return
	}
	prev, ok, err := f.Mail().ClaimNudgeUpTo(mail.Supervisor, newestID(es))
	if err != nil {
		f.requeueWake(es)
		f.stalledWake("admiral wake not claimed", "err", err, "queued", len(es))
		return
	}
	if !ok {
		f.requeueWake(es) // announced between the read above and the claim
		return
	}
	line := WakeLine(es)
	if err := f.typeText(v.Pane, line); err != nil {
		f.undelivered(es, prev, "paste failed: "+err.Error())
		return
	}
	if !f.guardEnter(v.Pane, line) {
		f.undelivered(es, prev, "the input box did not hold exactly the wake line")
	}
}

// canType reports whether the admiral pane may be typed into right now.
func (f *Fleet) canType(v AdmiralView) bool {
	return v.Pane != "" && v.Idle && v.Box && v.Input == "" && f.quiet()
}

// undelivered puts a wake that was typed but not submitted back on the
// queue and winds the claim back to prev, so the next poll types it again.
// An entry that has had WakeMaxTries goes no further: it stays on the badge
// and in the mailbox, and the give-up is logged as an error. Every case
// here is logged -- a report the admiral never sees is the bug this guards.
func (f *Fleet) undelivered(es []wakeEntry, prev, why string) {
	log := logx.For("mail")
	var keep, gone []wakeEntry
	for _, e := range es {
		e.tries++
		if e.tries >= WakeMaxTries {
			gone = append(gone, e)
			continue
		}
		keep = append(keep, e)
	}
	for _, e := range gone {
		log.Error("admiral wake given up after "+strconv.Itoa(e.tries)+" tries; the report is on the badge and in fleet_reports",
			"why", why, "agent", e.from, "state", e.state, "line", e.line)
	}
	if err := f.requeueWake(keep); err != nil {
		return // already logged as an error; the claim stays, nothing to retry
	}
	if len(keep) == 0 {
		return // nothing left to announce: the mark stays where it is
	}
	log.Warn("admiral wake not delivered, queued for a retry", "why", why, "queued", len(keep))
	if err := f.Mail().UnclaimNudgeTo(mail.Supervisor, prev); err != nil {
		// The entries are back on the queue but the mark still covers
		// them, so no poll will type them again: the badge is all the
		// admiral has left.
		log.Error("admiral wake claim not wound back; the queued reports will not be typed again",
			"err", err, "queued", len(keep))
	}
}

// WakeWarnEvery is how often a wake that cannot be delivered is logged; the
// poll runs every second, so every stall would otherwise flood the log.
var WakeWarnEvery = time.Minute

// stalledWake logs, at most once per WakeWarnEvery per process, that a
// queued wake is still sitting there.
func (f *Fleet) stalledWake(msg string, args ...any) {
	f.wakeMu.Lock()
	defer f.wakeMu.Unlock()
	if time.Since(f.wakeWarn) < WakeWarnEvery {
		return
	}
	f.wakeWarn = time.Now()
	logx.For("mail").Warn(msg, args...)
}

// guardEnter presses Enter only if the box holds exactly line, and reports
// whether it did. Otherwise it takes back what it typed (the box was seen
// empty just before) and leaves the user's characters: line plus text typed
// after it is deleted and that text typed again; a box ending in line, or
// showing only its start, loses len(line) characters. The report stays on
// the badge and on the queue.
func (f *Fleet) guardEnter(pane, line string) bool {
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
				return false
			}
			log.Info("woke admiral", "line", line)
			return true
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
	return false
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
