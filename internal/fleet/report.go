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
	m, err := f.Mail().Report(me, state, summary)
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
		note = f.autoClose(me)
	}
	if state == "done" || state == "blocked" || state == "question" {
		line := ReportLine(me, state, sum)
		if note != "" {
			line += " (" + note + ")"
		}
		f.wakeSupervisor(m.ID, me, state, line)
	}
	return note, nil
}

// UnreadReports counts the admiral's unread reports (DESIGN §6.7). It is
// what the overview header's ✉ badge and the tmux status line show; reading
// them with fleet_reports marks them read and the count falls to 0 (ADR-36).
func (f *Fleet) UnreadReports() int {
	n := 0
	for _, m := range f.Mail().Peek(mail.Admiral) {
		if m.Kind == mail.KindReport {
			n++
		}
	}
	return n
}

// wakeHeadline caps the summary in the line typed into the admiral pane,
// so the line fits on screen; the full text stays in the mailbox.
const wakeHeadline = 100

// ReportLine is the line typed into an idle admiral pane: a short headline
// and where the full text is (fleet_reports keeps it after the agent closes).
func ReportLine(from, state, summary string) string {
	if r := []rune(summary); len(r) > wakeHeadline {
		summary = string(r[:wakeHeadline-1]) + "…"
	}
	return fmt.Sprintf("[fleet] report from %s: %s: %s. Full text: call fleet_reports.", from, state, summary)
}

// autoClose closes a done agent when its profile opts in and it left no
// work behind (AgentBlocker). It returns the note for the admiral ("" = off).
func (f *Fleet) autoClose(name string) string {
	a, ok := f.Store.Load().Agents[name]
	if !ok || !f.Prefs.For(a.Profile).AutoCloseDone {
		return ""
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
