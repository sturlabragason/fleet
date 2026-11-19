package fleet

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/playbook"
	"github.com/sturlabragason/fleet/internal/todo"
	"github.com/sturlabragason/fleet/internal/tools"
)

// MaxDoneBounces is how many done reports fleet sends back to an agent
// before it hands the agent to the admiral as blocked (ADR-160).
const MaxDoneBounces = 2

// DoneRefused starts fleet's reply to a refused done; Report returns the
// reply as its note, so a caller shows it alone (Refused).
const DoneRefused = "[fleet] Your done report was not accepted: "

// Refused is whether Report's note is a refused done rather than a note on
// a filed report.
func Refused(note string) bool { return strings.HasPrefix(note, DoneRefused) }

// gateVerdict is what the done gate makes of a done report. state "" lets
// it through; otherwise the report is filed with state and summary instead,
// and reply, when set, goes back to the agent.
type gateVerdict struct {
	state, summary, reply string
}

// doneGate checks a done report against the agent's own work (ADR-160)
// and its queue (ADR-164): a done that leaves uncommitted changes, an
// unmerged branch or tweaks the agent has not had yet (left) goes back to
// the agent, at most MaxDoneBounces times, then to the admiral as blocked.
// An agent that may not merge and has only an unmerged branch left goes to
// the admiral as blocked at once. The work check lets through what fleet
// can't attribute: no spawn baseline, a checkout shared with another agent,
// git state it can't read, a remote agent and an agent the user is talking
// to. The queue check lets through a remote agent (fleet's reply may not
// reach it) and a done with a "queue:" line, the agent's stated exception
// (stop, safety, lost permission, urgent).
func (f *Fleet) doneGate(me, summary string, left []todo.Item) gateVerdict {
	b := f.Store.Load()
	a := b.Agents[me]
	if a == nil {
		return gateVerdict{}
	}
	merge := mayMerge(a)
	reason, reply := f.workLeft(b, a, summary), ""
	switch {
	case reason != "" && !merge && !strings.Contains(reason, "uncommitted changes"):
		return gateVerdict{state: "blocked", summary: fmt.Sprintf(
			"done, but %s and this agent may not merge (effort %s, no merge-local): merge its branch or grant merge-local. %s",
			reason, effortOf(a), summary)}
	case reason != "":
		reply = bounceText(reason, merge)
	case len(left) > 0 && a.Where == "" && !queueException(summary):
		reason = fmt.Sprintf("%d queued tweak(s) not handled yet", len(left))
		reply = queueText(left)
	}
	if reason == "" {
		if a.DoneBounces > 0 {
			f.resetBounces(me)
		}
		return gateVerdict{}
	}
	n, over := 0, false
	if err := f.Store.Update(func(b *board.Board) error {
		if a := b.Agents[me]; a != nil {
			if over = a.DoneBounces >= MaxDoneBounces; !over {
				a.DoneBounces++
			}
			n = a.DoneBounces
		}
		return nil
	}); err != nil {
		logx.For("fleet").Warn("done gate not recorded", "agent", me, "err", err)
		return gateVerdict{}
	}
	if over {
		return gateVerdict{state: "blocked", summary: fmt.Sprintf(
			"done refused %d times, work still left: %s. %s", n, reason, summary)}
	}
	logx.For("fleet").Info("done refused", "agent", me, "reason", reason, "bounce", n)
	return gateVerdict{
		state:   "progress",
		summary: fmt.Sprintf("done refused by fleet (%d/%d): %s. %s", n, MaxDoneBounces, reason, summary),
		reply:   reply,
	}
}

// workLeft is the agent's own work a done leaves behind ("" = none, or
// fleet can't attribute it).
func (f *Fleet) workLeft(b *board.Board, a *board.Agent, summary string) string {
	if a.Where != "" || a.Baseline == nil || superseded(summary) || sharedDir(b, a) || f.userBlocker(a) != "" {
		return ""
	}
	r := AgentBlocker(a)
	if r == "git state unreadable" || r == "no main or master branch" {
		return ""
	}
	return r
}

// queueException is a done whose summary has a "queue:" line: the agent
// reports done with tweaks open under one of the queue rule's exceptions.
func queueException(summary string) bool {
	for l := range strings.SplitSeq(summary, "\n") {
		if strings.HasPrefix(strings.TrimPrefix(strings.TrimSpace(l), "- "), "queue:") {
			return true
		}
	}
	return false
}

// queueText is fleet's reply to a done refused for open tweaks.
func queueText(left []todo.Item) string {
	var b strings.Builder
	b.WriteString(DoneRefused + "your queue still has tweaks you have not had yet; they reach you when this turn ends:")
	for _, i := range left {
		b.WriteString("\n- " + i.Line())
	}
	b.WriteString("\nWork them in order, then report done again. Only a stop, a safety issue, lost permission or an" +
		" urgent: message lets you report done with them open: then add a line \"queue: <which>\".")
	return b.String()
}

// resetBounces clears the refusal count once a done passes clean, so the
// agent's next task starts with its full MaxDoneBounces.
func (f *Fleet) resetBounces(me string) {
	if err := f.Store.Update(func(b *board.Board) error {
		if a := b.Agents[me]; a != nil {
			a.DoneBounces = 0
		}
		return nil
	}); err != nil {
		logx.For("fleet").Warn("done bounces not reset", "agent", me, "err", err)
	}
}

// bounceText is fleet's reply to a refused done: what is left and the steps
// that finish it.
func bounceText(reason string, merge bool) string {
	steps := "commit your work on your branch"
	if merge {
		steps = "commit your work, `fleet merge` it into local main"
	}
	return DoneRefused + reason + ". Finish it: " + steps +
		" (or report done with \"superseded by <X>\" if the work is not needed), then report done again." +
		" If you can't, report blocked to the admiral; don't ask the user."
}

// superseded is a done whose summary says its work was replaced, which
// passes the gate with the work left in place for the admiral to see.
func superseded(summary string) bool {
	return strings.Contains(strings.ToLower(summary), "superseded")
}

// sharedDir is an agent without a worktree of its own whose dir another
// agent on the board also works in: fleet can't tell whose files are whose.
func sharedDir(b *board.Board, a *board.Agent) bool {
	if a.Worktree != nil {
		return false
	}
	dir := filepath.Clean(a.Dir)
	for _, o := range b.Agents {
		if o.Name != a.Name && filepath.Clean(o.Dir) == dir {
			return true
		}
	}
	return false
}

// mayMerge is whether the agent may merge into local main itself: a pro
// playbook does, and any card that grants merge-local, push-main or release.
func mayMerge(a *board.Agent) bool {
	if effortOf(a) == playbook.Pro {
		return true
	}
	if a.Card == nil {
		return false
	}
	return slices.ContainsFunc(a.Card.May, grantsMerge)
}

// grantsMerge is whether one `may` entry lets the agent merge into local
// main: merge-local, push-main or release (ADR-129).
func grantsMerge(m string) bool {
	m = strings.ToLower(m)
	return m == "merge-local" || m == "push-main" || m == "release"
}

// effortOf is the agent's playbook level, with the spawn default for an
// agent that has none recorded.
func effortOf(a *board.Agent) playbook.Level {
	if a.Effort != "" {
		return playbook.Level(a.Effort)
	}
	return playbook.DefaultLevel(a.Worktree != nil)
}

// tell queues fleet's reply to an agent after its fleet_report has
// returned: on a goroutine in the MCP servers, else (the one-shot CLI, run
// inside the agent's own pane) through a detached `fleet send --queue`, so
// the pane is not typed into while the agent's command still runs.
func (f *Fleet) tell(name, text string) {
	if f.Tell == nil && f.Go == nil {
		cmd := "sleep 2; " + tools.Quote(f.Exe) + " send --queue " + tools.Quote(name) + " " + tools.Quote(text) + " >/dev/null 2>&1"
		if _, err := f.T.Run("run-shell", "-b", cmd); err != nil {
			logx.For("fleet").Warn("reply not delivered", "agent", name, "err", err)
		}
		return
	}
	send := f.Tell
	if send == nil {
		send = func(n, t string) error { _, err := f.Queue(n, t); return err }
	}
	job := func() {
		if err := send(name, text); err != nil {
			logx.For("fleet").Warn("reply not delivered", "agent", name, "err", err)
		}
	}
	if f.Go != nil {
		f.Go(job)
		return
	}
	job()
}
