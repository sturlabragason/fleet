package fleet

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/todo"
)

// Each agent session is a task in the todo store when a build has one
// (ADR-164): opened at spawn, adopt or resume, ended at close with its
// outcome. Every tweak to the agent goes under it as a queue item: a
// fleet_send (steered or queued), and the user's own prompts where fleet
// sees them (an ACP pane, a tool's prompt hook). A done report completes
// the tweaks the agent has had, adds its next: lines, and is refused while
// tweaks it has not had yet are open (donegate.go). A close hands what is
// left to the leader or the owning admiral. Without a store these are
// no-ops and the reports and notes are the only record.

// WorkDir is the dir an agent's context is inferred from: the repo a
// worktree was made from, else its dir.
func WorkDir(a *board.Agent) string {
	if a.Worktree != nil && a.Worktree.Repo != "" {
		return a.Worktree.Repo
	}
	return a.Dir
}

// todoDo runs fn on the store and logs a failure; the queue is a record
// beside the send, report or close, never a reason to fail one.
func todoDo(what, agent string, fn func(ctx context.Context, s todo.Store) error) {
	if _, err := TodoDo(fn); err != nil {
		logx.For("todo").Warn(what, "agent", agent, "err", err)
	}
}

// markSent records a text fleet types into the agent's pane, so its prompt
// hook does not take it for the user's (status.FleetSent).
func (f *Fleet) markSent(name, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if err := status.MarkSent(f.Home, name, text, time.Now()); err != nil {
		logx.For("todo").Warn("mark sent", "agent", name, "err", err)
	}
}

// UserPrompt records a prompt the agent's tool hook saw, unless fleet typed
// it: the user's own tweak, typed into the pane.
func (f *Fleet) UserPrompt(name, text string) {
	if text == "" || status.FleetSent(f.Home, name, text, time.Now()) {
		return
	}
	f.Tweak(name, todo.Prompt, "user: "+text)
}

// sessionGoal is the card goal, else the first line of the task.
func sessionGoal(a *board.Agent) string {
	if a.Card != nil && a.Card.Goal != "" {
		return a.Card.Goal
	}
	if g := todo.OneLine(a.Goal); g != "" {
		return g
	}
	return "session of " + a.Name
}

// sessionOpen starts the agent's session task (a no-op when one is open).
func (f *Fleet) sessionOpen(a *board.Agent) {
	if a == nil {
		return
	}
	dir := WorkDir(a)
	todoDo("session open", a.Name, func(ctx context.Context, s todo.Store) error {
		_, err := s.Open(ctx, todo.Session{Agent: a.Name, Admiral: a.Owner(), Leader: a.LeadBy,
			Goal: sessionGoal(a), Ctx: s.Context(dir), Dir: dir, Profile: a.Profile})
		return err
	})
}

// sessionOutcome is how a closed agent's session ended: its last report.
func sessionOutcome(a *board.Agent) string {
	if a == nil || a.Report == nil {
		return "closed without a report"
	}
	return a.Report.State + ": " + todo.OneLine(a.Report.Summary)
}

// sessionEnd closes the agent's session task with its outcome.
func (f *Fleet) sessionEnd(name, outcome string) {
	todoDo("session end", name, func(ctx context.Context, s todo.Store) error {
		return s.End(ctx, name, outcome)
	})
}

// Tweak records text sent to the agent as a queue item of kind (todo.Steer,
// todo.Queued or todo.Prompt) under its session, opening the session first
// for an agent spawned before the store had one. Fleet's own reply to a
// refused done is no tweak: `fleet report` sends it back through `fleet send
// --queue`, and recording it grew the queue with every refusal.
func (f *Fleet) Tweak(name, kind, text string) {
	a, ok := f.Store.Load().Agents[name]
	if !ok || strings.TrimSpace(text) == "" || Refused(text) {
		return
	}
	f.sessionOpen(a)
	todoDo("queue add", name, func(ctx context.Context, s todo.Store) error {
		_, err := s.Add(ctx, todo.Item{Queue: name, Kind: kind, Ctx: s.Context(WorkDir(a)), Description: text, Sessions: f.sessions(name)})
		return err
	})
}

// Deliver types text to agent name at once (now), for when its turn ends
// (queue) or into its running turn, and puts it on the agent's queue as a
// tweak (ADR-164): every text the user, the admiral or a leader sends is
// one. It returns the delivery (DeliveryNote).
func (f *Fleet) Deliver(name, text string, now, queue bool) (string, error) {
	var d string
	var err error
	switch {
	case now:
		err = f.SendNow(name, text)
	case queue:
		d, err = f.Queue(name, text)
	default:
		d, err = f.Steer(name, text)
	}
	if err == nil {
		f.Tweak(name, tweakKind(d), text)
		mode := d
		switch {
		case now:
			mode = acphost.DeliveryInterrupted // send now cancels the running turn (ADR-152)
		case d == "":
			mode = acphost.DeliverySent
		}
		f.Intervene(name, board.KindDeliver, mode)
	}
	return d, err
}

// tweakKind is the queue kind of a send that returned delivery d: a text
// held for later (by the agent's host) is queued; one that reached the
// agent, queue=true to an idle agent included, is a steer.
func tweakKind(d string) string {
	if d == acphost.DeliveryQueued || d == DeliveryHeld {
		return todo.Queued
	}
	return todo.Steer
}

// queueClose runs on a done report, before the done gate: it completes the
// tweaks the agent has had by now and returns those it has not had yet.
// A steer or a prompt reached it when sent; a queued tweak reaches it when
// its turn ends, so it has had those added before its previous report or
// the start of this turn, whichever is later, and those its ACP host's
// transcript shows sent (a held prompt starts the next turn at once, so
// the board may never see the turn change). A refused done is no report
// here: the agent is still in the turn the gate sent it back to.
func (f *Fleet) queueClose(name string) (left []todo.Item) {
	a, ok := f.Store.Load().Agents[name]
	if !ok {
		return nil
	}
	var since time.Time
	if a.Report != nil && a.DoneBounces == 0 {
		since = a.Report.At
	}
	if a.Status == "working" && a.StatusSince.After(since) {
		since = a.StatusSince
	}
	now := time.Now()
	by := f.sessions(name)
	tx := acphost.TranscriptPath(f.Home, name)
	todoDo("queue done", name, func(ctx context.Context, s todo.Store) error {
		is, err := s.Queue(ctx, name)
		if err != nil {
			return err
		}
		for _, i := range is {
			if !todo.Tweak(i.Kind) {
				continue
			}
			cut := now
			if i.Kind == todo.Queued {
				cut = since
				if acphost.SentSince(tx, i.Entry, i.Description) {
					cut = now
				}
			}
			if !i.Entry.Before(cut) {
				left = append(left, i)
				continue
			}
			if _, err := s.Done(ctx, i.UUID, by...); err != nil {
				return err
			}
		}
		return nil
	})
	return left
}

// queueNext adds each "next:" line of an accepted done report to the queue.
func (f *Fleet) queueNext(name, summary string) {
	a, ok := f.Store.Load().Agents[name]
	nexts := nextLines(summary)
	if !ok || len(nexts) == 0 {
		return
	}
	todoDo("queue next", name, func(ctx context.Context, s todo.Store) error {
		c, by := s.Context(WorkDir(a)), f.sessions(name)
		for _, n := range nexts {
			if _, err := s.Add(ctx, todo.Item{Queue: name, Kind: todo.Next, Ctx: c, Description: n, Sessions: by}); err != nil {
				return err
			}
		}
		return nil
	})
}

// nextLines are a report's "next:" lines (a leading "- " allowed), text only.
func nextLines(summary string) []string {
	var out []string
	for l := range strings.SplitSeq(summary, "\n") {
		l = strings.TrimPrefix(strings.TrimSpace(l), "- ")
		if v, ok := strings.CutPrefix(l, "next:"); ok && strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

// queueHand moves a closed agent's queued tweaks, which its host may never
// have delivered, to its leader's queue, or to its admiral as followups that
// fleet_brief shows. Everything else is completed instead (ADR-177,
// ADR-188): a steer or a prompt reached the agent when sent, so handing it
// over only echoed the admiral's own steers back as followups, and a next:
// line is in the agent's report already.
func (f *Fleet) queueHand(name, leader, admiral string) {
	todoDo("queue complete delivered", name, func(ctx context.Context, s todo.Store) error {
		is, err := s.Queue(ctx, name)
		if err != nil {
			return err
		}
		for _, i := range is {
			if i.Kind != todo.Steer && i.Kind != todo.Prompt && i.Kind != todo.Next {
				continue
			}
			if _, err := s.Done(ctx, i.UUID); err != nil {
				return err
			}
		}
		return nil
	})
	todoDo("queue hand-over", name, func(ctx context.Context, s todo.Store) error {
		n, err := s.Hand(ctx, name, leader, admiral)
		if n > 0 {
			logx.For("todo").Info("queue handed over", "agent", name, "items", n, "leader", leader, "admiral", admiral)
		}
		return err
	})
}

// QueueCounts is each agent's pending queue length; nil without a store.
func QueueCounts() map[string]int {
	var n map[string]int
	todoDo("queue counts", "", func(ctx context.Context, s todo.Store) error {
		var err error
		n, err = s.Queues(ctx)
		return err
	})
	return n
}

// queueInfo is fleet_info's session and queue lines: "" without a store.
func queueInfo(name string) (session, queue string) {
	var is []todo.Item
	var sess todo.Item
	var open bool
	ok, err := TodoDo(func(ctx context.Context, s todo.Store) error {
		var err error
		if sess, open, err = s.SessionOf(ctx, name); err != nil {
			return err
		}
		is, err = s.Queue(ctx, name)
		return err
	})
	switch {
	case err != nil:
		return "", "unreadable (" + err.Error() + ")"
	case !ok:
		return "", ""
	}
	if open {
		session = sess.Line()
	}
	if len(is) == 0 {
		return session, "empty"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d pending (fleet_note done=<id> closes one)", len(is))
	for _, i := range is {
		b.WriteString("\n  " + i.Line())
	}
	return session, b.String()
}
