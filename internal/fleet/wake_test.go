package fleet

import (
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/supervisor"
)

func ptr(b bool) *bool { return &b }

// fakeAdmiral is an admiral pane with an input box: TypeText appends to the
// box, Enter submits it, BSpace deletes one rune.
type fakeAdmiral struct {
	mu        sync.Mutex
	idle      *bool
	noBox     bool
	box       string
	submitted []string
	onSubmit  func(string)
	beforeTyp func(a *fakeAdmiral) // runs inside TypeText, before the paste lands
}

// fakeAdm installs a fake admiral on f; submitted lines also go to pasted
// as "%sup <line>".
func fakeAdm(f *Fleet, pasted *[]string, idle *bool) *fakeAdmiral {
	a := &fakeAdmiral{idle: idle}
	if pasted != nil {
		a.onSubmit = func(l string) { *pasted = append(*pasted, "%sup "+l) }
	}
	f.SupPane = func() AdmiralView {
		a.mu.Lock()
		defer a.mu.Unlock()
		return AdmiralView{Pane: "%sup", Idle: *a.idle, Box: !a.noBox, Input: a.box}
	}
	f.TypeText = func(pane, text string) error {
		if a.beforeTyp != nil {
			a.beforeTyp(a)
		}
		a.mu.Lock()
		a.box += text
		a.mu.Unlock()
		return nil
	}
	f.PressKeys = func(pane string, keys ...string) error {
		a.mu.Lock()
		var sub []string
		if len(keys) == 3 && keys[0] == "-l" && keys[1] == "--" {
			a.box += keys[2]
			keys = nil
		}
		for _, k := range keys {
			switch k {
			case "Enter":
				a.submitted = append(a.submitted, a.box)
				sub = append(sub, a.box)
				a.box = ""
			case "BSpace":
				if r := []rune(a.box); len(r) > 0 {
					a.box = string(r[:len(r)-1])
				}
			}
		}
		a.mu.Unlock()
		for _, l := range sub {
			if a.onSubmit != nil {
				a.onSubmit(l)
			}
		}
		return nil
	}
	return a
}

func TestWakeNeverTypesOverUserInput(t *testing.T) {
	f, pasted := mailFleet(t)
	a := fakeAdm(f, pasted, ptr(true))
	a.box = "can we review the fleet? wha"
	f.Report("a", "blocked", "need creds")
	f.DeliverWake()
	if a.box != "can we review the fleet? wha" || len(a.submitted) != 0 {
		t.Fatalf("touched user input: box %q submitted %q", a.box, a.submitted)
	}
	// No box on screen (unknown layout): never type either.
	a.box, a.noBox = "", true
	f.DeliverWake()
	if len(a.submitted) != 0 {
		t.Fatalf("typed without seeing the box: %q", a.submitted)
	}
	a.noBox = false
	f.DeliverWake()
	if want := ReportLine("a", "blocked", "need creds"); len(a.submitted) != 1 || a.submitted[0] != want {
		t.Fatalf("submitted %q, want %q", a.submitted, want)
	}
}

func TestWakeCoalescesQueuedReports(t *testing.T) {
	f, pasted := mailFleet(t)
	a := fakeAdm(f, pasted, ptr(true))
	a.box = "typing"
	f.Report("a", "done", "x")
	f.Report("b", "blocked", "y")
	f.Report("a", "question", "z")
	a.box = ""
	f.DeliverWake()
	want := "[fleet] 3 new reports: a (question), b (blocked). Full text: call fleet_reports."
	if len(a.submitted) != 1 || a.submitted[0] != want {
		t.Fatalf("submitted %q, want %q", a.submitted, want)
	}
}

// The user starts typing between the empty-box check and the paste: Enter
// is not pressed and only the pasted line is removed.
func TestWakeEnterGuard(t *testing.T) {
	f, pasted := mailFleet(t)
	GuardWait = 100 * time.Millisecond
	t.Cleanup(func() { GuardWait = 1500 * time.Millisecond })
	a := fakeAdm(f, pasted, ptr(false))
	f.Report("a", "done", "x")
	*a.idle = true
	a.beforeTyp = func(a *fakeAdmiral) { a.mu.Lock(); a.box = "on the acme sum"; a.mu.Unlock() }
	f.DeliverWake()
	if a.box != "on the acme sum" || len(a.submitted) != 0 {
		t.Fatalf("box %q submitted %q", a.box, a.submitted)
	}
	// Typed after the paste: fleet's line is removed and the user's
	// characters typed again; still no Enter.
	f.Mail().Inbox(mail.Supervisor, false, 0)
	a.box, a.beforeTyp = "", nil
	f.TypeText = func(pane, text string) error { a.mu.Lock(); a.box += text + " -- abc"; a.mu.Unlock(); return nil }
	f.Report("b", "done", "y")
	if a.box != " -- abc" || len(a.submitted) != 0 {
		t.Fatalf("typed after: box %q submitted %q", a.box, a.submitted)
	}
	// The box never shows more than the start of the line: removed.
	f.Mail().Inbox(mail.Supervisor, false, 0)
	a.box = ""
	f.TypeText = func(pane, text string) error { a.mu.Lock(); a.box += text; a.mu.Unlock(); return nil }
	f.SupPane = func() AdmiralView {
		a.mu.Lock()
		defer a.mu.Unlock()
		in := a.box
		if r := []rune(in); len(r) > 10 {
			in = string(r[:10])
		}
		return AdmiralView{Pane: "%sup", Idle: true, Box: true, Input: in}
	}
	f.Report("c", "done", "z")
	if a.box != "" || len(a.submitted) != 0 {
		t.Fatalf("partial: box %q submitted %q", a.box, a.submitted)
	}
}

// A paste that fails is retried on the next poll.
func TestWakeRetriesFailedPaste(t *testing.T) {
	f, pasted := mailFleet(t)
	a := fakeAdm(f, pasted, ptr(true))
	ok := f.TypeText
	f.TypeText = func(pane, text string) error { return os.ErrClosed }
	f.Report("a", "blocked", "x")
	f.TypeText = ok
	f.DeliverWake()
	if len(a.submitted) != 1 {
		t.Fatalf("not retried: %q", a.submitted)
	}
}

// The admiral is busy when the report lands and never reads its inbox: the
// wake must be typed as soon as the pane goes idle, and every later report
// must wake it again instead of being swallowed by the first one's claim.
// This is the poll (the watcher's housekeeping) doing the retry.
func TestWakeRetriedWhileReportsStayUnread(t *testing.T) {
	f, pasted := mailFleet(t)
	busy := ptr(false)
	a := fakeAdm(f, pasted, busy)
	f.Report("a", "done", "x")
	if len(a.submitted) != 0 {
		t.Fatalf("typed into a busy admiral: %q", a.submitted)
	}
	*busy = true
	f.DeliverWake() // the poll
	want := ReportLine("a", "done", "x")
	if len(a.submitted) != 1 || a.submitted[0] != want {
		t.Fatalf("not retried when idle: %q", a.submitted)
	}
	// Nothing was read, so the queue stays claimed for that batch; a poll
	// with no new report types nothing more.
	f.DeliverWake()
	if len(a.submitted) != 1 {
		t.Fatalf("woke twice for one report: %q", a.submitted)
	}
	// A second report, with the first still unread, is its own batch.
	f.Report("b", "blocked", "y")
	want = ReportLine("b", "blocked", "y")
	if len(a.submitted) != 2 || a.submitted[1] != want {
		t.Fatalf("second report not delivered: %q", a.submitted)
	}
	// A third while the admiral is busy again, then idle: still delivered.
	*busy = false
	f.Report("a", "question", "z")
	*busy = true
	f.DeliverWake()
	want = ReportLine("a", "question", "z")
	if len(a.submitted) != 3 || a.submitted[2] != want {
		t.Fatalf("third report not delivered: %q", a.submitted)
	}
}

// A wake that was typed but not submitted (the user typed into the box) is
// queued again, not dropped: the next poll with an empty box delivers it.
func TestWakeUnsubmittedIsRequeued(t *testing.T) {
	f, pasted := mailFleet(t)
	GuardWait = 50 * time.Millisecond
	t.Cleanup(func() { GuardWait = 1500 * time.Millisecond })
	a := fakeAdm(f, pasted, ptr(true))
	a.beforeTyp = func(a *fakeAdmiral) { a.mu.Lock(); a.box = "mid sentence"; a.mu.Unlock() }
	f.Report("a", "done", "x")
	if len(a.submitted) != 0 {
		t.Fatalf("submitted over user input: %q", a.submitted)
	}
	a.mu.Lock()
	a.box, a.beforeTyp = "", nil
	a.mu.Unlock()
	f.DeliverWake()
	want := ReportLine("a", "done", "x")
	if len(a.submitted) != 1 || a.submitted[0] != want {
		t.Fatalf("wake dropped instead of requeued: %q", a.submitted)
	}
}

func TestBadge(t *testing.T) {
	f, pasted := mailFleet(t)
	fakeAdm(f, pasted, ptr(false))
	f.Report("a", "done", "x")
	f.Report("b", "blocked", "y")
	f.Report("a", "progress", "z")
	f.DeliverWake()
	got, _ := f.T.Run("show-options", "-gqv", BadgeOption)
	if strings.TrimSpace(got) != "3 reports: a, b" {
		t.Fatalf("badge %q", got)
	}
	f.Mail().Inbox(mail.Supervisor, false, 0)
	f.DeliverWake()
	if got, _ := f.T.Run("show-options", "-gqv", BadgeOption); strings.TrimSpace(got) != "" {
		t.Fatalf("badge after read %q", got)
	}
}

// End to end on a real tmux pane: text typed into the admiral's box is
// never touched; once the box is empty the wake is typed and submitted.
func TestWakeRealPane(t *testing.T) {
	f, _ := mailFleet(t)
	supervisor.Save(f.Home, supervisor.Session{Profile: "cat"}) // patterns "fake"
	pane, err := f.T.Spawn(Session, "main", f.Home, nil, "sh -c 'stty -icanon; printf \"> \"; cat >/dev/null'")
	if err != nil {
		t.Fatal(err)
	}
	f.T.SetPaneOption(pane, "@role", "sup")
	for i := 0; i < 50 && !f.supPane().Idle; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	f.T.SendKeys(pane, "-l", "half typed")
	for i := 0; i < 50 && f.supPane().Input == ""; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	f.Report("a", "blocked", "need creds")
	f.DeliverWake()
	if v := f.supPane(); v.Input != "half typed" {
		t.Fatalf("user input changed to %q", v.Input)
	}
	// A fresh admiral with an empty box gets the wake, submitted.
	f.T.KillPane(pane)
	if pane, err = f.T.Spawn(Session, "main", f.Home, nil, "sh -c 'stty -icanon; printf \"> \"; cat >/dev/null'"); err != nil {
		t.Fatal(err)
	}
	f.T.SetPaneOption(pane, "@role", "sup")
	for i := 0; i < 50 && !f.supPane().Idle; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	f.DeliverWake()
	out, _ := f.T.Capture(pane, 0)
	cx, _ := f.T.Run("display-message", "-p", "-t", pane, "#{cursor_x}")
	if !strings.Contains(out, "> "+ReportLine("a", "blocked", "need creds")) || strings.TrimSpace(cx) != "0" {
		t.Fatalf("wake not typed and submitted (cursor_x %s):\n%s", cx, out)
	}
}

// A box fleet types into but can never read back is not typed into for
// ever: after WakeMaxTries it gives up and leaves the report to the badge.
func TestWakeGivesUpAfterMaxTries(t *testing.T) {
	f, pasted := mailFleet(t)
	GuardWait = 20 * time.Millisecond
	t.Cleanup(func() { GuardWait = 1500 * time.Millisecond })
	a := fakeAdm(f, pasted, ptr(true))
	typed := 0
	f.TypeText = func(pane, text string) error {
		typed++
		a.mu.Lock()
		a.box = "something else entirely"
		a.mu.Unlock()
		return nil
	}
	f.Report("a", "done", "x") // first try
	for i := 0; i < 5; i++ {
		a.mu.Lock()
		a.box = ""
		a.mu.Unlock()
		f.DeliverWake()
	}
	if typed != WakeMaxTries {
		t.Fatalf("typed %d times, want %d", typed, WakeMaxTries)
	}
	if len(a.submitted) != 0 {
		t.Fatalf("submitted %q", a.submitted)
	}
	// The report is still unread and still on the badge.
	if f.UnreadReports() != 1 {
		t.Fatalf("unread %d, want 1", f.UnreadReports())
	}
}

// Only one process types into the admiral pane at a time.
func TestWakeOneTyperAtATime(t *testing.T) {
	f, pasted := mailFleet(t)
	a := fakeAdm(f, pasted, ptr(true))
	release, ok := f.lockWake()
	if !ok {
		t.Fatal("lock not taken")
	}
	if _, held := f.lockWake(); held {
		release()
		t.Fatal("lock taken twice")
	}
	f.Report("a", "done", "x")
	if len(a.submitted) != 0 {
		t.Fatalf("typed while another process held the pane: %q", a.submitted)
	}
	release()
	f.DeliverWake()
	if len(a.submitted) != 1 {
		t.Fatalf("not delivered after the lock went: %q", a.submitted)
	}
}

// The queue line is the format every build understands: four fields, no
// try counter (that lives in a file of its own), so a fleet from before
// ADR-51 reading this queue during a rollout still types the right line.
func TestParseWakeFormats(t *testing.T) {
	unread := []mail.Msg{{ID: "001"}, {ID: "002"}}
	raw := strings.Join([]string{
		"001\ta\tdone\t[fleet] report from a",
		"002\tb\tblocked\t[fleet] report from b",
		"003\tc\tdone\tread already", // report is no longer unread
		"nonsense",
		"",
	}, "\n")
	got := parseWake([]byte(raw), unread)
	want := []wakeEntry{
		{id: "001", from: "a", state: "done", line: "[fleet] report from a"},
		{id: "002", from: "b", state: "blocked", line: "[fleet] report from b"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWake = %+v, want %+v", got, want)
	}
	e := wakeEntry{id: "002", from: "b", state: "blocked", tries: 2, line: "[fleet] report from b"}
	if l := e.raw(); l != "002\tb\tblocked\t[fleet] report from b" {
		t.Fatalf("raw() = %q", l)
	}
}

// The try counter survives in its own file, so a report typed by one
// process and left unsubmitted is not retried for ever by the next.
func TestWakeTriesPersistAcrossProcesses(t *testing.T) {
	f, _ := mailFleet(t)
	es := []wakeEntry{{id: "001", from: "a", state: "done", tries: 2, line: "x"}}
	f.writeTries(es)
	if got := f.readTries()["001"]; got != 2 {
		t.Fatalf("tries = %d, want 2", got)
	}
	f.writeTries(nil)
	if _, err := os.Stat(f.triesPath()); !os.IsNotExist(err) {
		t.Fatalf("counter file outlived the queue: %v", err)
	}
}
