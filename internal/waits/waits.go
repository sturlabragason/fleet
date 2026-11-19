// Package waits is the "waits on you" list (ADR-167): everything fleet knows
// is waiting on the user, oldest first, one line each. It is pure data built
// from the board, the todo store (ADR-164), the admirals' unread reports and
// what extensions add (threads), so the overview's w view and the web
// mirror's /api/waits draw the same list. Each item also carries its
// section, context and parent, so the w view's tree (ADR-168) can group the
// list together with the work that does not wait on the user: running, open
// and quiet agents and threads.
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
	Working  = "working"   // an agent at work (section Running)
	Idle     = "idle"      // an agent idle with nothing asked (Open or Quiet)
)

// Sections of the tree, in the order of the user's attention
// (docs/design/thread-board.md). Paused threads come from extensions only.
const (
	Waiting = "waiting"
	Running = "running"
	Open    = "open"
	Quiet   = "quiet"
	Paused  = "paused"
)

// Sections is every section in rank order.
var Sections = []string{Waiting, Running, Open, Quiet, Paused}

// QuietAfter is how long an idle agent sits before it counts as Quiet.
const QuietAfter = 48 * time.Hour

// Item is one thing waiting on the user.
type Item struct {
	Kind   string    `json:"kind"`
	Source string    `json:"source"`          // agent or admiral it comes from
	Agent  string    `json:"agent,omitempty"` // the agent to show or answer; "" for an admiral's own item
	Ask    string    `json:"ask"`             // one line
	Since  time.Time `json:"since"`
	Ref    string    `json:"ref,omitempty"` // todo or thread id
	// Section is one of Sections; Build sets Waiting where it is empty.
	Section string `json:"section,omitempty"`
	Ctx     string `json:"ctx,omitempty"`    // the user's context (ADR-164)
	Parent  string `json:"parent,omitempty"` // the agent, thread or admiral it belongs to
}

// Todo reports whether d (mark done) applies: an item of the todo store.
func (i Item) Todo() bool { return i.Ref != "" && i.Kind != Thread }

// Only is the items of one section; Only(items, Waiting) is the flat
// "waits on you" list.
func Only(items []Item, section string) []Item {
	var out []Item
	for _, it := range items {
		if it.Section == section {
			out = append(out, it)
		}
	}
	return out
}

// Answerable reports whether the overview's answer key applies to it: an
// agent on a prompt or an open question.
func (i Item) Answerable() bool {
	return i.Agent != "" && (i.Kind == Prompt || i.Kind == Question || i.Kind == Blocked)
}

// Input is what Build reads. Todos holds both admiral items and queue
// items; Reports holds the unread reports of every admiral box; Ctx is each
// agent's context, by name.
type Input struct {
	Agents  []*board.Agent
	Todos   []todo.Item
	Reports []mail.Msg
	Extra   []Item
	Ctx     map[string]string
}

// Build is the list, oldest first. A todo filed twice with the same text for
// the same owner shows once (the older), and an unread report is left out
// when the agent's own open question or block already shows it. An agent
// that waits on nothing is a Running, Open or Quiet row.
func Build(in Input, now time.Time) []Item {
	var out []Item
	asking := map[string]bool{}
	for _, a := range in.Agents {
		it, ok := agentItem(a, now)
		if !ok {
			it = agentRow(a, now)
		}
		out = append(out, it)
		if it.Kind == Question || it.Kind == Blocked {
			asking[a.Name] = true
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
	for i := range out {
		fill(&out[i], in.Ctx)
	}
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

// agentRow is an agent that waits on nothing: at work, else Open (idle, or
// in error with its status as the kind) until it has been still so long it
// has gone quiet.
func agentRow(a *board.Agent, now time.Time) Item {
	it := Item{Kind: cmp.Or(a.Status, Idle), Section: Open, Source: a.Name, Agent: a.Name, Ask: todo.OneLine(a.Goal), Since: a.StatusSince}
	switch {
	case a.Status == Working:
		it.Section = Running
	case !a.StatusSince.IsZero() && now.Sub(a.StatusSince) >= QuietAfter:
		it.Section = Quiet
	}
	if it.Ask == "" {
		it.Ask = a.Status
	}
	return it
}

func todoItem(t todo.Item) Item {
	if t.Queue != "" {
		return Item{Kind: Queue, Source: t.Queue, Agent: t.Queue, Ask: t.Kind + ": " + todo.OneLine(t.Description), Since: t.Entry, Ref: t.ID(), Ctx: t.Ctx}
	}
	return Item{Kind: t.Kind, Source: t.Admiral, Ask: todo.OneLine(t.Description), Since: t.Entry, Ref: t.ID(), Ctx: t.Ctx}
}

// fill gives an item its defaults: section Waiting, the agent (else the
// source) as parent, and the agent's context.
func fill(it *Item, ctx map[string]string) {
	if it.Section == "" {
		it.Section = Waiting
	}
	if it.Parent == "" {
		it.Parent = cmp.Or(it.Agent, it.Source)
	}
	if it.Ctx == "" {
		it.Ctx = ctx[it.Agent]
	}
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
