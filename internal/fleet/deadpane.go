package fleet

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// deadTail is how many non-empty screen lines a crash report carries.
const deadTail = 8

// deadWakeLine caps the screen line quoted in a crash wake line.
const deadWakeLine = 80

type deadReaper struct {
	run   sync.Mutex
	mu    sync.Mutex
	tried map[string]time.Time // last close attempt per agent
}

type deadJob struct {
	a     *board.Agent
	pane  tmux.PaneInfo
	fresh bool // first sighting: claim, report, then close if due
}

// reapDead handles agents whose pane is dead (DESIGN §6.7, ADR-33): a first
// sighting is claimed on the board and reported to the admiral; a clean exit
// closes at once, a crash after its profile's dead_pane_grace. A close is
// retried at most once per sweepEvery. Git and capture run in the
// background, one run at a time; a busy run leaves the rest for a later poll.
// off holds every dead pane for the outage (ADR-110): no report, no close.
func (f *Fleet) reapDead(as []*board.Agent, panes map[string]tmux.PaneInfo, now time.Time, off bool) {
	var jobs []deadJob
	f.dead.mu.Lock()
	if f.dead.tried == nil {
		f.dead.tried = map[string]time.Time{}
	}
	for _, a := range as {
		p, ok := panes[a.Name]
		switch {
		case !ok:
		case a.ACP() && (a.Status == string(status.Parked) || (!a.Closing.IsZero() && now.Sub(a.Closing) < acpClosingFor)):
			// A park or close in progress: an acp host exits on its stop
			// request just before fleet kills its window.
		case a.Exit == nil && f.holdDead(a, p, off, now):
		case a.Exit == nil:
			jobs = append(jobs, deadJob{a: a, pane: p, fresh: true})
		case off:
		case closeDue(a.Exit, f.Prefs.For(a.Profile).DeadPaneGrace, now) && now.Sub(f.dead.tried[a.Name]) >= sweepEvery:
			jobs = append(jobs, deadJob{a: a, pane: p})
		}
	}
	f.dead.mu.Unlock()
	if len(jobs) == 0 || !f.dead.run.TryLock() {
		return
	}
	go func() {
		defer f.dead.run.Unlock()
		for _, j := range jobs {
			if j.fresh {
				f.reportDead(j.a, j.pane)
			} else {
				f.closeDead(j.a.Name)
			}
		}
	}()
}

// reportDead claims the first sighting of a's death on the board and, if
// this call claimed it, reports it to the admiral and closes when due.
func (f *Fleet) reportDead(a *board.Agent, p tmux.PaneInfo) {
	log := logx.For("fleet")
	e := exitOf(p, time.Now())
	claimed := false
	err := f.Store.Update(func(b *board.Board) error {
		if x, ok := b.Agents[a.Name]; ok && x.Exit == nil {
			x.Exit, claimed = e, true
		}
		return nil
	})
	if err != nil {
		log.Warn("dead pane claim failed", "agent", a.Name, "err", err)
		return
	}
	if !claimed {
		return
	}
	var screen string
	if a.ACP() {
		screen = f.acpScreen(a.Name, 50)
	} else {
		screen, _ = f.T.Capture(p.ID, 50) // some history too: the last lines may have scrolled off
	}
	grace := f.Prefs.For(a.Profile).DeadPaneGrace
	state, text, line := deadReport(a.Name, e, screen, AgentBlocker(a), grace)
	log.Info("dead pane", "agent", a.Name, "state", state, "exit", exitHow(e))
	up := f.ReportBox(a.Name)
	if m, err := f.Mail().ReportTo(a.Name, up, state, text); err != nil {
		// No unread mail: ClaimNudge would drop the wake line anyway.
		log.Error("dead pane report failed", "agent", a.Name, "err", err)
	} else {
		f.wakeBox(up, m.ID, a.Name, state, line)
	}
	if closeDue(e, grace, e.At) {
		f.closeDead(a.Name)
	}
}

func (f *Fleet) closeDead(name string) {
	f.dead.mu.Lock()
	f.dead.tried[name] = time.Now()
	f.dead.mu.Unlock()
	log := logx.For("fleet")
	if err := f.closeLater(name); err != nil {
		log.Warn("dead pane close failed", "agent", name, "err", err)
		return
	}
	log.Info("closing dead pane", "agent", name)
}

// closeDue: clean exits close at once, crashes once dead for grace
// (prefs.GraceOff never).
func closeDue(e *board.Exit, grace time.Duration, now time.Time) bool {
	return e.Clean() || (grace >= 0 && now.Sub(e.At) >= grace)
}

// exitOf reads the exit from tmux's pane_dead_status/pane_dead_signal.
func exitOf(p tmux.PaneInfo, at time.Time) *board.Exit {
	e := &board.Exit{At: at}
	if c, err := strconv.Atoi(strings.TrimSpace(p.DeadStatus)); err == nil {
		e.Code = &c
	}
	e.Signal, _ = strconv.Atoi(strings.TrimSpace(p.DeadSignal))
	return e
}

func exitHow(e *board.Exit) string {
	switch {
	case e.Signal != 0:
		return "signal " + strconv.Itoa(e.Signal)
	case e.Code != nil:
		return "exit " + strconv.Itoa(*e.Code)
	}
	return "exit unknown"
}

// deadReport builds the mailbox report and the admiral wake line for a dead
// pane. work is AgentBlocker's reason ("" = nothing left behind).
func deadReport(name string, e *board.Exit, screen, work string, grace time.Duration) (state, text, line string) {
	var note, wakeNote string
	if work != "" {
		note = "\nleft work behind: " + work + "; the session is kept, `fleet adopt` can resume it"
		wakeNote = " Left work behind: " + work + "; the session is kept, fleet adopt can resume it."
	}
	if e.Clean() {
		return "done", "exited, closed" + note, "[fleet] " + name + " exited, closed." + wakeNote
	}
	head := "crashed (" + exitHow(e) + ")"
	lines := deadLines(screen)
	text = head
	if len(lines) > 0 {
		text += "\n" + strings.Join(lines, "\n")
	}
	line = "[fleet] " + name + " " + head
	if len(lines) > 0 {
		last := mail.FoldSummary(lines[len(lines)-1])
		if r := []rune(last); len(r) > deadWakeLine {
			last = string(r[:deadWakeLine-1]) + "…"
		}
		line += ": " + strings.TrimRight(last, ".")
	}
	if !strings.HasSuffix(line, "…") {
		line += "."
	}
	return "blocked", text + note, line + " " + closing(grace) + wakeNote + " Full text: call fleet_reports."
}

// deadLines is the last deadTail non-empty screen lines without tmux's
// "Pane is dead (" banner.
func deadLines(screen string) []string {
	var out []string
	for _, l := range status.Tail(screen, deadTail+2) {
		if !strings.HasPrefix(strings.TrimSpace(l), "Pane is dead (") {
			out = append(out, l)
		}
	}
	if len(out) > deadTail {
		out = out[len(out)-deadTail:]
	}
	return out
}

func closing(grace time.Duration) string {
	switch {
	case grace < 0:
		return "Not auto-closing (dead_pane_grace = off)."
	case grace == 0:
		return "Closing now."
	}
	return "Closing in " + shortDur(grace) + "."
}

// shortDur drops zero tails: 5m0s -> 5m, 1h0m0s -> 1h.
func shortDur(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
