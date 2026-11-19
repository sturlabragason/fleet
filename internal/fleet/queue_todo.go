package fleet

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/todo"
)

// Each agent's queue lives in the todo store when a build has one (ADR-164):
// a fleet_send queue=true adds an item, a done report completes the items
// it had been given and adds its next: lines, and a close hands what is
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

// queueAdd records text sent with queue=true as the agent's next item.
func (f *Fleet) queueAdd(name, text string) {
	a, ok := f.Store.Load().Agents[name]
	if !ok {
		return
	}
	todoDo("queue add", name, func(ctx context.Context, s todo.Store) error {
		_, err := s.Add(ctx, todo.Item{Queue: name, Kind: todo.Queued, Ctx: s.Context(WorkDir(a)), Description: text})
		return err
	})
}

// queueDone runs on a done report: it completes the queued items the agent
// had been given by then (added before its previous report or the start of
// this turn) and adds each "next:" line of the report.
func (f *Fleet) queueDone(name string, a board.Agent, prevReport time.Time, summary string) {
	since := prevReport
	if a.Status == "working" && a.StatusSince.After(since) {
		since = a.StatusSince
	}
	nexts := nextLines(summary)
	todoDo("queue done", name, func(ctx context.Context, s todo.Store) error {
		if _, err := s.Finish(ctx, name, since); err != nil {
			return err
		}
		c := s.Context(WorkDir(&a))
		for _, n := range nexts {
			if _, err := s.Add(ctx, todo.Item{Queue: name, Kind: todo.Next, Ctx: c, Description: n}); err != nil {
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

// queueHand moves a closed agent's pending items to its leader's queue, or
// to its admiral as followups that fleet_brief shows.
func (f *Fleet) queueHand(name, leader, admiral string) {
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

// queueInfo is fleet_info's queue lines: "" without a store.
func queueInfo(name string) string {
	var is []todo.Item
	ok, err := TodoDo(func(ctx context.Context, s todo.Store) error {
		var err error
		is, err = s.Queue(ctx, name)
		return err
	})
	switch {
	case err != nil:
		return "unreadable (" + err.Error() + ")"
	case !ok:
		return ""
	case len(is) == 0:
		return "empty"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d pending (fleet_note done=<id> closes one)", len(is))
	for _, i := range is {
		b.WriteString("\n  " + i.Line())
	}
	return b.String()
}
