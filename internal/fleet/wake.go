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

	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/status"
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

// WakeSettle is how long a new wake waits for the next report before it is
// typed: reports that land close together wake the admiral once, in one
// line, and so cost one turn instead of one each (ADR-93). 0 types at once.
var WakeSettle = 30 * time.Second

// WakeMaxHold caps the wait, counted from the oldest report not yet
// announced, so a steady trickle of reports can't hold a wake back.
var WakeMaxHold = 90 * time.Second

// wakeNow is the clock the settle window reads; tests move it.
var wakeNow = time.Now

// BadgeOption is the tmux user option the status line shows (`fleet up`).
const BadgeOption = "@fleet_reports"

// wakeSupervisor queues a report for the admiral pane and tries to deliver
// it now. Queued reports are coalesced into one line (WakeLine).
// id is the report's mail id: an entry is typed only while it is unread.
func (f *Fleet) wakeSupervisor(id, from, state, line string) {
	f.wakeBox(mail.Supervisor, id, from, state, line)
}

// wakeBox is the same for any inbox that has a pane behind it: the admiral,
// or a leader whose squad member reported (ADR-53). One queue, one try
// counter and one lock per box, all under mail/<box>/.
func (f *Fleet) wakeBox(box, id, from, state, line string) {
	p := f.wakePath(box)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if fh, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fh.WriteString(wakeEntry{id: id, from: from, state: state, line: line}.raw() + "\n")
		fh.Close()
	}
	f.DeliverWakeTo(box)
}

func (f *Fleet) wakePath(box string) string {
	return filepath.Join(f.Home, "mail", box, "wake-pending")
}

// wakeEntry is one queued report: its mail id, who sent it, the state, how
// many times fleet has typed it without it being submitted, and the line.
type wakeEntry struct {
	id, from, state, line string
	tries                 int
}

// raw is the queue line: id, from, state, line, tab-separated. The try
// count is deliberately not in it: a fleet binary from before ADR-51 reads
// the fourth field as the whole line, and during a rollout both builds read
// this file, so an extra field would be typed into the admiral pane.
func (e wakeEntry) raw() string {
	clean := func(s string) string { return strings.NewReplacer("\t", " ", "\n", " ").Replace(s) }
	return strings.Join([]string{clean(e.id), clean(e.from), clean(e.state), clean(e.line)}, "\t")
}

// parseWake reads the queue, keeping entries whose report is still unread.
// Lines in the pre-ADR-36 format (one bare line) are dropped.
func parseWake(b []byte, unread []mail.Msg) []wakeEntry {
	ids := map[string]bool{}
	for _, m := range unread {
		ids[m.ID] = true
	}
	var out []wakeEntry
	for _, l := range strings.Split(string(b), "\n") {
		p := strings.SplitN(l, "\t", 4)
		if len(p) < 4 || !ids[p[0]] {
			continue
		}
		out = append(out, wakeEntry{id: p[0], from: p[1], state: p[2], line: p[3]})
	}
	return out
}

// triesPath holds how often each queued report has been typed without being
// submitted. It is a file of its own so the queue keeps the format every
// build understands; a missing or unreadable counter only costs a retry.
func (f *Fleet) triesPath(box string) string { return f.wakePath(box) + "-tries" }

func (f *Fleet) readTries(box string) map[string]int {
	out := map[string]int{}
	b, err := os.ReadFile(f.triesPath(box))
	if err != nil {
		return out
	}
	for _, l := range strings.Split(string(b), "\n") {
		p := strings.SplitN(l, "\t", 2)
		if len(p) != 2 {
			continue
		}
		if n, err := strconv.Atoi(p[1]); err == nil {
			out[p[0]] = n
		}
	}
	return out
}

// writeTries replaces the counter file with the counts for es, forgetting
// ids that have left the queue. It runs under lockWake.
func (f *Fleet) writeTries(box string, es []wakeEntry) {
	var sb strings.Builder
	for _, e := range es {
		if e.tries > 0 {
			fmt.Fprintf(&sb, "%s\t%d\n", e.id, e.tries)
		}
	}
	if sb.Len() == 0 {
		os.Remove(f.triesPath(box))
		return
	}
	os.MkdirAll(filepath.Dir(f.triesPath(box)), 0o755)
	if err := os.WriteFile(f.triesPath(box), []byte(sb.String()), 0o644); err != nil {
		logx.For("mail").Warn("wake try counter not written", "box", box, "err", err)
	}
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
func (f *Fleet) takeWake(box string) []wakeEntry {
	p := f.wakePath(box)
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
	es := parseWake(b, f.Mail().Peek(box))
	tries := f.readTries(box)
	for i := range es {
		es[i].tries = tries[es[i].id]
	}
	return es
}

// requeueWake puts entries back at the end of the queue. A failure here is
// the one way a report can be lost, so it is logged as an error.
func (f *Fleet) requeueWake(box string, es []wakeEntry) error {
	if len(es) == 0 {
		return nil
	}
	fh, err := os.OpenFile(f.wakePath(box), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
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
		logx.For("mail").Error("wake lost: could not put it back on the queue",
			"box", box, "err", err, "queued", len(es), "line", wakeLineTo(box, es))
	}
	return err
}

// lockWake takes the cross-process claim on typing into the admiral pane.
// The queue file keeps two processes from typing the same report, but not
// from typing two different ones into the same box at the same time: the
// reporting agent's own process and the watcher poll a second apart. false
// means somebody else holds it and this process types nothing.
func (f *Fleet) lockWake(box string) (release func(), ok bool) {
	p := f.wakePath(box) + ".lock"
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
func WakeLine(es []wakeEntry) string { return wakeLineTo(mail.Supervisor, es) }

// wakeLineTo is WakeLine for one mailbox: a leader reads its squad's
// reports with fleet_squad_reports, not fleet_reports (ADR-53).
func wakeLineTo(box string, es []wakeEntry) string {
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
	if mail.IsAdmiralBox(box) {
		for _, e := range es {
			if !mail.ValidState(e.state) { // mail, not a report: both tools
				return fmt.Sprintf("[fleet] %d new items: %s. Read them with fleet_reports and fleet_inbox.", len(es), strings.Join(names, ", "))
			}
		}
	}
	return fmt.Sprintf("[fleet] %d new reports: %s. Full text: call %s.", len(es), strings.Join(names, ", "), reportsTool(box))
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
func (f *Fleet) DeliverWake() { f.deliverWake(true) }

// RetryWake is DeliverWake without the badge: for the overview while a
// watcher is live. The badge has one writer, the watcher, which re-execs
// on a rebuild; two writers whose code disagrees on what is unread set it
// in turn every poll and the badge blinks (ADR-75).
func (f *Fleet) RetryWake() { f.deliverWake(false) }

func (f *Fleet) deliverWake(badge bool) {
	for _, box := range f.AdmiralBoxes() {
		f.deliverWakeTo(box, badge)
	}
	for _, l := range f.Leaders() {
		f.deliverWakeTo(l, badge)
	}
}

// DeliverWakeTo delivers one box's queue: the admiral's, or a leader's
// (ADR-53). Everything below is the same for both; only the badge is the
// admiral's alone.
func (f *Fleet) DeliverWakeTo(box string) { f.deliverWakeTo(box, true) }

func (f *Fleet) deliverWakeTo(box string, badge bool) {
	unread := f.unreadFor(box)
	if badge && box == mail.Supervisor {
		f.syncBadge()
	}
	b, err := os.ReadFile(f.wakePath(box))
	if err != nil {
		return
	}
	es := parseWake(b, unread)
	if len(es) == 0 {
		f.requeueWake(box, f.takeWake(box)) // compacts: entries whose report was read go
		return
	}
	// Already announced: the admiral was told about every queued report and
	// has not read them yet. Leave the queue alone -- no rename, no log --
	// so the next new report coalesces these into its line.
	mark := f.Mail().WakeMark(box)
	if newestID(es) <= mark {
		return
	}
	// Still settling: the watcher's next poll looks again. Admirals only;
	// leaders are agents and keep their own wake policy.
	if f.isAdmiralBox(box) && settling(es, unread, mark, wakeNow()) {
		return
	}
	if !f.canType(f.boxView(box)) {
		f.stalledWake("wake waiting for an idle, empty input box", "box", box, "queued", len(es))
		return
	}
	release, ok := f.lockWake(box)
	if !ok {
		return // another process is typing into the pane
	}
	defer release()
	// The pane is read again under the lock: whoever held it may have just
	// typed into the box, or taken these very entries.
	v := f.boxView(box)
	if !f.canType(v) {
		return
	}
	es = f.takeWake(box)
	if len(es) == 0 {
		return
	}
	prev, ok, err := f.Mail().ClaimWakeUpTo(box, newestID(es))
	if err != nil {
		f.requeueWake(box, es)
		f.stalledWake("wake not claimed", "box", box, "err", err, "queued", len(es))
		return
	}
	if !ok {
		f.requeueWake(box, es) // announced between the read above and the claim
		return
	}
	line := wakeLineTo(box, es)
	if f.isACP(box) {
		if _, err := f.acpCall(box, acphost.Request{Op: acphost.OpPrompt, Text: line}); err != nil {
			f.undelivered(box, es, prev, "acp prompt failed: "+err.Error())
			return
		}
		f.writeTries(box, nil)
		return
	}
	if err := f.typeText(v.Pane, line); err != nil {
		f.undelivered(box, es, prev, "paste failed: "+err.Error())
		return
	}
	if !f.guardEnter(box, v.Pane, line) {
		f.undelivered(box, es, prev, "the input box did not hold exactly the wake line")
		return
	}
	f.writeTries(box, nil) // delivered: the counters go with the entries
}

// settling reports whether the entries not yet announced (id after mark)
// should wait for more: the newest arrived less than WakeSettle ago and the
// oldest less than WakeMaxHold ago (ADR-93). The times are the reports' own,
// from the mailbox, so every process that delivers wakes agrees on them.
func settling(es []wakeEntry, unread []mail.Msg, mark string, now time.Time) bool {
	if WakeSettle <= 0 {
		return false
	}
	at := map[string]time.Time{}
	for _, m := range unread {
		at[m.ID] = m.T
	}
	var oldest, newest time.Time
	for _, e := range es {
		t := at[e.id]
		if e.id <= mark || t.IsZero() {
			continue
		}
		if oldest.IsZero() || t.Before(oldest) {
			oldest = t
		}
		if t.After(newest) {
			newest = t
		}
	}
	return !newest.IsZero() && now.Sub(newest) < WakeSettle && now.Sub(oldest) < WakeMaxHold
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
func (f *Fleet) undelivered(box string, es []wakeEntry, prev, why string) {
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
		log.Error("wake given up after "+strconv.Itoa(e.tries)+" tries; the report is on the badge and in fleet_reports",
			"why", why, "agent", e.from, "state", e.state, "line", e.line)
	}
	if err := f.requeueWake(box, keep); err != nil {
		return // already logged as an error; the claim stays, nothing to retry
	}
	f.writeTries(box, keep)
	if len(keep) == 0 {
		return // nothing left to announce: the mark stays where it is
	}
	log.Warn("wake not delivered, queued for a retry", "box", box, "why", why, "queued", len(keep))
	if err := f.Mail().UnclaimWakeTo(box, prev); err != nil {
		// The entries are back on the queue but the mark still covers
		// them, so no poll will type them again: the badge is all the
		// admiral has left.
		log.Error("wake claim not wound back; the queued reports will not be typed again",
			"box", box, "err", err, "queued", len(keep))
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
func (f *Fleet) guardEnter(box, pane, line string) bool {
	log := logx.For("mail")
	want := squash(line)
	var raw, got string
	end := time.Now().Add(GuardWait)
	for {
		v := f.boxView(box)
		raw = v.Input
		got = squash(raw)
		if v.Pane == pane && v.Box && got == want {
			if err := f.pressKeys(pane, "Enter"); err != nil {
				// The line is in the box: take it back, or the box never
				// reads empty again and every retry waits on it.
				f.pressKeys(pane, backspaces(len([]rune(line)))...)
				log.Warn("wake failed", "box", box, "err", err)
				return false
			}
			log.Info("woke inbox owner", "box", box, "line", line)
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
			log.Warn("wake: could not retype user text", "box", box, "err", err)
		}
		how = "removed, user text after it kept"
	case strings.HasSuffix(got, want), got != "" && strings.HasPrefix(want, got):
		f.pressKeys(pane, backspaces(n)...)
		how = "removed"
	}
	log.Warn("wake not submitted: input box did not hold exactly the wake line", "box", box, "wake", how)
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
func (f *Fleet) syncBadge() {
	var all []mail.Msg
	for _, box := range f.AdmiralBoxes() {
		all = append(all, f.Mail().Peek(box)...)
	}
	changed := f.setOpt(BadgeOption, Badge(all))
	multi := ""
	if len(f.AdmiralNames()) > 1 {
		multi = "1"
	}
	f.syncAdmiralPanes()
	if f.setOpt(BadgeByOption, f.BadgeBy()) || f.setOpt(MultiOption, multi) || changed {
		f.T.Run("refresh-client", "-S")
	}
}

// setOpt sets a global tmux option, or unsets it for "", and reports a change.
func (f *Fleet) setOpt(name, want string) bool {
	have, _ := f.T.Run("show-options", "-gqv", name)
	if strings.TrimSpace(have) == strings.TrimSpace(want) {
		return false
	}
	if want == "" {
		f.T.Run("set-option", "-gu", name)
	} else {
		f.T.Run("set-option", "-g", name, want)
	}
	return true
}

// supPane finds the @role=sup pane in fleet:main and reads it with the
// admiral profile's status patterns (default copilot).
func (f *Fleet) supPane() AdmiralView { return f.admiralView(board.Main) }

// boxView is the pane fleet may type a wake into for an inbox: the admiral
// pane for the admiral, the agent's own pane for a leader (ADR-53). A
// leader is read with its own profile's status patterns, so an agent that
// is busy or sitting on a prompt is never typed into.
func (f *Fleet) boxView(box string) AdmiralView {
	if mail.IsAdmiralBox(box) {
		return f.admiralView(mail.AdmiralOf(box))
	}
	a, ok := f.Store.Load().Agents[box]
	if !ok || !a.Leader {
		return AdmiralView{}
	}
	pane := f.paneOf(box)
	if pane == "" {
		return AdmiralView{}
	}
	if a.ACP() {
		// No input box to read: the host takes whole prompts (ADR-74).
		return AdmiralView{Pane: pane, Idle: a.Status == string(status.Idle), Box: true}
	}
	text, err := f.T.Capture(pane, 0)
	if err != nil {
		return AdmiralView{Pane: pane}
	}
	ps := f.setOf(box)
	st, ok := ps.Detect(text)
	in, hasBox := ps.InputBox(text)
	return AdmiralView{Pane: pane, Idle: ok && st == status.Idle, Box: hasBox, Input: in}
}
