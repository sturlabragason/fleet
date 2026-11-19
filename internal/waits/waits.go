// Package waits is the "waits on you" list (ADR-167): everything fleet knows
// is waiting on the user, oldest first, one line each. It is pure data built
// from the board, the todo store (ADR-164), the admirals' unread reports and
// what extensions add (threads), so the overview's w view and the web
// mirror's /api/waits draw the same list.
package waits

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/todo"
)

// Kinds of item that come from an agent's state or mail. Todo items keep
// their todo kind (ask, promise, request, followup); a queue item is Queue.
const (
	Prompt   = "prompt"    // a prompt or picker is on the agent's screen
	Question = "question"  // its newest report is an unanswered question
	Blocked  = "blocked"   // its newest report is an unanswered blocked
	NoReport = "no report" // idle after a turn and never reported
	Report   = "report"    // an unread report in an admiral's box
	Queue    = "queue"     // a pending item in an agent's queue
	Thread   = "thread"    // an open thread with a next step (extensions)
)

// Item is one thing waiting on the user.
type Item struct {
	Kind   string    `json:"kind"`
	Source string    `json:"source"`          // agent or admiral it comes from
	Agent  string    `json:"agent,omitempty"` // the agent to show or answer; "" for an admiral's own item
	Ask    string    `json:"ask"`             // one line
	Since  time.Time `json:"since"`
	Ref    string    `json:"ref,omitempty"` // todo or thread id
}

// Answerable reports whether the overview's answer key applies to it: an
// agent on a prompt or an open question.
func (i Item) Answerable() bool {
	return i.Agent != "" && (i.Kind == Prompt || i.Kind == Question || i.Kind == Blocked)
}

// Input is what Build reads. Todos holds both admiral items and queue
// items; Reports holds the unread reports of every admiral box.
type Input struct {
	Agents  []*board.Agent
	Todos   []todo.Item
	Reports []mail.Msg
	Extra   []Item
}

// Build is the list, oldest first. A todo filed twice with the same text for
// the same owner shows once (the older), and an unread report is left out
// when the agent's own open question or block already shows it.
func Build(in Input, now time.Time) []Item {
	var out []Item
	asking := map[string]bool{}
	for _, a := range in.Agents {
		if it, ok := agentItem(a, now); ok {
			out = append(out, it)
			if it.Kind == Question || it.Kind == Blocked {
				asking[a.Name] = true
			}
		}
	}
	for _, m := range in.Reports {
		if m.Kind != mail.KindReport || asking[m.From] {
			continue
		}
		out = append(out, Item{Kind: Report, Source: m.From, Agent: m.From, Ask: todo.OneLine(m.Text), Since: m.T})
	}
	for _, t := range in.Todos {
		out = append(out, todoItem(t))
	}
	out = append(out, in.Extra...)
	out = dedupe(out)
	slices.SortStableFunc(out, func(a, b Item) int {
		return cmp.Or(a.Since.Compare(b.Since), cmp.Compare(a.Source, b.Source), cmp.Compare(a.Ask, b.Ask))
	})
	return out
}

// agentItem is what the agent waits on the user for, if anything.
func agentItem(a *board.Agent, now time.Time) (Item, bool) {
	it := Item{Source: a.Name, Agent: a.Name, Since: a.StatusSince}
	switch {
	case a.OpenAsk():
		it.Kind, it.Ask, it.Since = a.Report.State, todo.OneLine(a.Report.Summary), a.Report.At
	case a.Status == "waiting":
		it.Kind, it.Ask = Prompt, todo.OneLine(a.LastLine)
		if it.Ask == "" {
			it.Ask = "prompt on screen"
		}
	case a.NoReport(now) || a.UserNoReport(now):
		it.Kind, it.Ask = NoReport, "idle, no report"
		if g := todo.OneLine(a.Goal); g != "" {
			it.Ask += ": " + g
		}
	default:
		return Item{}, false
	}
	return it, true
}

func todoItem(t todo.Item) Item {
	if t.Queue != "" {
		return Item{Kind: Queue, Source: t.Queue, Agent: t.Queue, Ask: t.Kind + ": " + todo.OneLine(t.Description), Since: t.Entry, Ref: t.ID()}
	}
	return Item{Kind: t.Kind, Source: t.Admiral, Ask: todo.OneLine(t.Description), Since: t.Entry, Ref: t.ID()}
}

// dedupe keeps the oldest of items with the same kind, owner and text
// (case and spacing ignored).
func dedupe(in []Item) []Item {
	best := map[string]int{}
	out := make([]Item, 0, len(in))
	for _, it := range in {
		k := it.Kind + "\x00" + it.Source + "\x00" + strings.ToLower(strings.Join(strings.Fields(it.Ask), " "))
		if i, ok := best[k]; ok {
			if it.Since.Before(out[i].Since) {
				out[i] = it
			}
			continue
		}
		best[k] = len(out)
		out = append(out, it)
	}
	return out
}
