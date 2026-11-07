package fleet

import (
	"fmt"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/tools"
)

// Report records an agent's report to the admiral (DESIGN §6.7): a
// "report" message in the admiral's mailbox and the latest report on the
// board. done, blocked and question wake an idle admiral. On done with
// auto_close_done, note is "auto-closing" or "not auto-closed: <reason>".
func (f *Fleet) Report(me, state, summary string) (note string, err error) {
	if err := f.onBoard(me); err != nil {
		return "", err
	}
	// A squad member reports to its leader, everyone else to the admiral
	// (ADR-53). The leader's own reports still go up to the admiral.
	up := f.ReportBox(me)
	m, err := f.Mail().ReportTo(me, up, state, summary)
	if err != nil {
		return "", err
	}
	sum := mail.FoldSummary(strings.TrimPrefix(m.Text, state+": "))
	err = f.Store.Update(func(b *board.Board) error {
		if a, ok := b.Agents[me]; ok {
			a.Report = &board.Report{State: state, Summary: sum, At: time.Now()}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if state == "done" {
		// With a done-check the verdict decides, so the wake waits for it
		// (ADR-43). Where Go is set the check runs off this goroutine, so
		// the agent's fleet_report returns at once; otherwise (the one-shot
		// `fleet report` CLI, and tests) it runs here and reports its note.
		if job, ok := f.claimCheck(me); ok {
			if f.Go != nil {
				f.Go(func() { f.doneCheck(job, m.ID, sum) })
				return "done-check running: " + job.cmd, nil
			}
			return f.doneCheck(job, m.ID, sum), nil
		}
		// No claim: either this agent has no check, or one is already
		// running or has run. A verdict that failed keeps the agent open
		// however many times it reports done.
		switch st := f.checkStanding(me); st {
		case checkNone:
			note = f.autoClose(me)
		case checkRunning:
			return "done-check running", nil
		case checkFailed:
			note = "not auto-closed: done-check failed"
		case checkPassed:
			note = f.autoClose(me)
		}
	}
	if state == "done" || state == "blocked" || state == "question" {
		line := ReportLineTo(up, me, state, sum)
		if note != "" {
			line += " (" + note + ")"
		}
		f.wakeBox(up, m.ID, me, state, line)
	}
	return note, nil
}

// staleCheck is how long a claim is honoured before a sweep may take it:
// the runner's own worst case plus a margin it can never reach, so the
// sweep never races a live runner into a second run of the command.
var staleCheck = CheckTimeout + checkWaitDelay + 30*time.Second

// checkStanding is where an agent's done-check stands when no fresh claim
// could be taken, so Report knows whether the verdict allows an auto-close.
type checkStanding int

const (
	checkNone checkStanding = iota // no card, or no done-check
	checkRunning
	checkPassed
	checkFailed
)

func (f *Fleet) checkStanding(me string) checkStanding {
	a, ok := f.Store.Load().Agents[me]
	if !ok || a.Card == nil || a.Card.Check == "" {
		return checkNone
	}
	switch {
	case a.Card.Checked == nil:
		return checkRunning
	case a.Card.Checked.Pass:
		return checkPassed
	default:
		return checkFailed
	}
}

// checkJob is one claimed done-check run: which agent, which incarnation of
// it (a name can be closed and re-spawned), and the command to run.
type checkJob struct {
	name, dir, profile, cmd string
	want                    int
	created                 time.Time
	claim                   time.Time
}

// claimCheck claims the one done-check run an agent's card gets (ADR-43).
// It is false when there is no check, when one has already finished, and
// when another runner holds a claim that has not gone stale — a claim older
// than the whole timeout means its runner died with the check unfinished.
func (f *Fleet) claimCheck(me string) (checkJob, bool) {
	var job checkJob
	ok := false
	now := time.Now()
	err := f.Store.Update(func(b *board.Board) error {
		a := b.Agents[me]
		if a == nil || a.Card == nil || a.Card.Check == "" || a.Card.Checked != nil {
			return nil
		}
		if !a.Card.Started.IsZero() && now.Sub(a.Card.Started) < staleCheck {
			return nil
		}
		a.Card.Started = now
		job = checkJob{name: a.Name, dir: a.Dir, profile: a.Profile, cmd: a.Card.Check, want: a.Card.Want, created: a.Created, claim: now}
		ok = true
		return nil
	})
	if err != nil {
		logx.For("fleet").Warn("done-check not claimed", "agent", me, "err", err)
		return job, false
	}
	return job, ok
}

// doneCheck runs a claimed done-check, records the verdict on the board,
// then wakes the admiral with it and decides the auto-close (ADR-43). A
// failing check never auto-closes: the work is not finished. It returns the
// auto-close note, for the caller that ran it inline.
func (f *Fleet) doneCheck(job checkJob, msgID, sum string) string {
	res := RunCheck(job.dir, &board.Card{Check: job.cmd, Want: job.want}, CheckTimeout)
	logx.For("fleet").Info("done-check", "agent", job.name, "cmd", job.cmd,
		"pass", res.Pass, "code", res.Code, "err", res.Err)
	recorded := false
	if err := f.Store.Update(func(b *board.Board) error {
		cur := b.Agents[job.name]
		// The agent may have been closed and the name re-spawned while the
		// check ran: a stale verdict must not land on fresh work.
		if cur == nil || cur.Card == nil || !cur.Created.Equal(job.created) || !cur.Card.Started.Equal(job.claim) {
			return nil
		}
		cur.Card.Checked = res
		recorded = true
		return nil
	}); err != nil {
		logx.For("fleet").Warn("done-check not recorded", "agent", job.name, "err", err)
	}
	if !recorded {
		logx.For("fleet").Info("done-check dropped (agent gone or replaced)", "agent", job.name)
		return ""
	}
	note := ""
	switch {
	case res.Pass:
		note = f.autoClose(job.name)
	case f.Prefs.For(job.profile).AutoCloseDone:
		note = "not auto-closed: done-check failed"
	}
	line := ReportLineTo(f.ReportBox(job.name), job.name, "done", sum) + " Done-check: " + res.Verdict() + "."
	if note != "" {
		line += " (" + note + ")"
	}
	f.wakeBox(f.ReportBox(job.name), msgID, job.name, "done", line)
	return note
}

// sweepChecks runs done-checks that nobody is running: an agent that
// reported done and whose check was never claimed, or whose runner died
// with it (ADR-43). The overview calls it on every refresh, so a verdict is
// never lost when the agent's own process goes away.
func (f *Fleet) sweepChecks(agents []*board.Agent) {
	for _, a := range agents {
		if a.Card == nil || a.Card.Check == "" || a.Card.Checked != nil {
			continue
		}
		if a.Report == nil || a.Report.State != "done" {
			continue
		}
		job, ok := f.claimCheck(a.Name)
		if !ok {
			continue
		}
		sum, id := a.Report.Summary, f.lastReportID(a.Name)
		if f.Go != nil {
			f.Go(func() { f.doneCheck(job, id, sum) })
			continue
		}
		f.doneCheck(job, id, sum)
	}
}

// lastReportID is the admiral's newest unread report message from name, so a
// swept check's wake is dropped once the admiral has read that report.
func (f *Fleet) lastReportID(name string) string {
	id := ""
	for _, m := range f.Mail().Peek(f.AdmiralBoxOf(name)) {
		if m.Kind == mail.KindReport && m.From == name {
			id = m.ID
		}
	}
	return id
}

// UnreadReports counts the admiral's unread reports (DESIGN §6.7). It is
// what the overview header's ✉ badge and the tmux status line show; reading
// them with fleet_reports marks them read and the count falls to 0 (ADR-36).
func (f *Fleet) UnreadReports() int {
	n := 0
	for _, c := range f.UnreadBy() {
		n += c
	}
	return n
}

// wakeHeadline caps the summary in the line typed into the admiral pane,
// so the line fits on screen; the full text stays in the mailbox.
const wakeHeadline = 100

// ReportLine is the line typed into an idle admiral pane: a short headline
// and where the full text is (fleet_reports keeps it after the agent closes).
func ReportLine(from, state, summary string) string {
	return ReportLineTo(mail.Supervisor, from, state, summary)
}

// ReportLineTo is ReportLine for one mailbox. A leader has no fleet_reports
// — its tool is fleet_squad_reports — so the line names the tool its reader
// actually has (DESIGN §6.11, ADR-53).
func ReportLineTo(box, from, state, summary string) string {
	if r := []rune(summary); len(r) > wakeHeadline {
		summary = string(r[:wakeHeadline-1]) + "…"
	}
	return fmt.Sprintf("[fleet] report from %s: %s: %s. Full text: call %s.", from, state, summary, reportsTool(box))
}

// reportsTool is the tool the owner of box reads its reports with.
func reportsTool(box string) string {
	if mail.IsAdmiralBox(box) {
		return "fleet_reports"
	}
	return "fleet_squad_reports"
}

// autoClose closes a done agent when its profile opts in, the user is not
// talking to it (ADR-61) and it left no work behind (AgentBlocker). It
// returns the note for the admiral ("" = off).
func (f *Fleet) autoClose(name string) string {
	a, ok := f.Store.Load().Agents[name]
	if !ok || !f.Prefs.For(a.Profile).AutoCloseDone {
		return ""
	}
	if reason := f.userBlocker(a); reason != "" {
		logx.For("fleet").Info("not auto-closed", "agent", name, "reason", reason)
		return "not auto-closed: " + reason
	}
	if reason := AgentBlocker(a); reason != "" {
		return "not auto-closed: " + reason
	}
	if err := f.closeLater(name); err != nil {
		logx.For("fleet").Warn("auto-close failed", "agent", name, "err", err)
		return "not auto-closed: " + err.Error()
	}
	logx.For("fleet").Info("auto-closing", "agent", name)
	return "auto-closing"
}

// closeLater closes name through tmux after 2 s, so the reporting MCP call
// (which runs inside the agent) returns first.
func (f *Fleet) closeLater(name string) error {
	if f.CloseLater != nil {
		return f.CloseLater(name)
	}
	_, err := f.T.Run("run-shell", "-b", "sleep 2; "+tools.Quote(f.Exe)+" close "+tools.Quote(name)+" >/dev/null 2>&1")
	return err
}

// CloseBlocker returns why dir can't be auto-closed ("" = it can): it must
// be a git work tree with a clean status whose HEAD is merged into the
// local main (or master when there is no main).
func CloseBlocker(dir string) string {
	g := GitInfo(dir)
	switch {
	case !g.Repo:
		return "not a git work tree"
	case g.Dirty != 0:
		return "uncommitted changes"
	case g.Base == "":
		return "no main or master branch"
	case !g.Merged:
		return "not merged into " + g.Base
	}
	return ""
}
