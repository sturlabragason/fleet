package mcp

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/todo"
)

// The admiral's promises, open asks and the user's requests in flight are
// items in the todo store when the build has one (ADR-164), so a fresh admiral
// finds them in fleet_brief whatever the handoff says.

// briefTodoMax bounds the brief's open-items section.
const briefTodoMax = 2 << 10

// noteTask adds the admiral task a fleet_note asks for (open=, or text
// starting "promise:", "ask:" or "request:") and says so; "" for a plain note.
func (s *Server) noteTask(a args) string {
	kind, text := todo.Kind(a.Text)
	if a.Open != "" {
		kind = a.Open
		if k, rest := todo.Kind(a.Text); k != "" {
			text = rest
		}
	}
	if kind == "" {
		return ""
	}
	it, ok, err := s.F.AddItem(s.Me(), kind, text, a.Agent, a.Ctx)
	switch {
	case err != nil:
		return "; not added to the todo store: " + err.Error()
	case !ok && a.Open == "":
		return "" // a plain note that happens to start "ask:"; nothing changes without a store
	case !ok:
		return "; no todo store in this build, so it is in the notes only"
	}
	return fmt.Sprintf("; open task [%s] (fleet_note done=%s closes it)", it.ID(), it.ID())
}

// noteDone is fleet_note done=<id>.
func (s *Server) noteDone(a args) (string, error) {
	it, err := s.F.DoneItem(s.Me(), a.Agent, a.Done, a.Text)
	if err != nil {
		return "", err
	}
	return "closed task [" + it.ID() + "] " + it.Description, nil
}

// handoffTasks records fleet_handoff's open_items ("ask: …", "promise: …",
// "request: …"; no prefix is a request) and returns the lines the handoff
// gets, so the list is in the handoff too. It is idempotent (ADR-188): a
// line that starts with an open item's "[id]", as the brief shows it, keeps
// that item; one whose id is no longer open is not filed again; and AddItem
// returns an open item that says the same instead of a duplicate.
func (s *Server) handoffTasks(items []string) string {
	var open []todo.Item
	checked, err := fleet.TodoDo(func(c context.Context, st todo.Store) error {
		var err error
		open, err = st.Admiral(c, s.Me())
		return err
	})
	checked = checked && err == nil
	var b strings.Builder
	for _, raw := range items {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if id, ok := itemRef(raw); ok {
			if !checked {
				fmt.Fprintf(&b, "\n- %s (not checked: the todo store is unavailable)", raw)
				continue
			}
			if i := slices.IndexFunc(open, func(it todo.Item) bool { return strings.HasPrefix(it.UUID, id) }); i >= 0 {
				b.WriteString("\n- " + open[i].Line())
			} else {
				fmt.Fprintf(&b, "\n- %s (not open any more: not filed again)", raw)
			}
			continue
		}
		kind, text := todo.Kind(raw)
		if kind == "" {
			kind = todo.Request
		}
		it, ok, err := s.F.AddItem(s.Me(), kind, text, "", "")
		switch {
		case err != nil:
			// The handoff must still be written: the item goes in it as text.
			fmt.Fprintf(&b, "\n- %s: %s (not stored: %v)", kind, text, err)
		case ok:
			b.WriteString("\n- " + it.Line())
		default:
			b.WriteString("\n- " + kind + ": " + text)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n\nOpen items:" + b.String()
}

// itemRe is a line that starts "[id]", as Item.Line writes it; a tag such
// as "[ADR-12]" or "[blocked]" is not an id.
var itemRe = regexp.MustCompile(`^\[([0-9a-f]{4,})\]`)

// itemRef is the id a line starts with.
func itemRef(line string) (string, bool) {
	m := itemRe.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// briefFresh is how old an item may be and still be listed ahead of the
// older ones in the brief.
const briefFresh = 72 * time.Hour

// briefItemRunes cuts an item's text in the brief; file=todo has it whole.
const briefItemRunes = 120

// kindRank orders the brief's open items: what waits on the user first.
var kindRank = map[string]int{todo.Ask: 0, todo.Promise: 1, todo.Request: 2, todo.Followup: 3}

// rankItems is the admiral's items for the brief (ADR-188): one per text
// (todo.Same; the duplicates' ids are returned for closing), the user's
// items before followups, the last briefFresh before older ones, then asks,
// promises and requests, newest first.
func rankItems(its []todo.Item, now time.Time) (out []todo.Item, dups map[string][]string) {
	its = slices.Clone(its)
	slices.SortStableFunc(its, func(a, b todo.Item) int { return b.Entry.Compare(a.Entry) })
	dups = map[string][]string{}
	for _, it := range its {
		if i := slices.IndexFunc(out, func(o todo.Item) bool { return todo.Same(o, it) }); i >= 0 {
			dups[out[i].UUID] = append(dups[out[i].UUID], it.ID())
			continue
		}
		out = append(out, it)
	}
	key := func(it todo.Item) (int, int, int) {
		f, old := 0, 0
		if it.Kind == todo.Followup {
			f = 1
		}
		if now.Sub(it.Entry) > briefFresh {
			old = 1
		}
		return f, old, kindRank[it.Kind]
	}
	slices.SortStableFunc(out, func(a, b todo.Item) int {
		af, ao, ak := key(a)
		bf, bo, bk := key(b)
		return cmp.Or(cmp.Compare(af, bf), cmp.Compare(ao, bo), cmp.Compare(ak, bk))
	})
	return out, dups
}

// itemLines is rankItems' items, one line each, with their duplicates' ids;
// short drops the sessions and cuts the text, so the brief lists more.
func itemLines(ranked []todo.Item, dups map[string][]string, short bool) []string {
	out := make([]string, len(ranked))
	for i, it := range ranked {
		if short {
			it.Sessions = nil
			if r := []rune(it.Description); len(r) > briefItemRunes {
				it.Description = string(r[:briefItemRunes-1]) + "…"
			}
		}
		out[i] = it.Line()
		if d := dups[it.UUID]; len(d) > 0 {
			out[i] += " (also open as " + strings.Join(d, ", ") + ": close those)"
		}
	}
	return out
}

// todoBrief is fleet_brief's "Open asks / in flight": the admiral's pending
// items ranked (rankItems) up to briefTodoMax, with a count of the cut ones
// by kind, then the queues of its agents; "" without a store.
func (s *Server) todoBrief() string {
	mine, queues, ok, err := s.todos()
	const h = "## Open asks / in flight (fleet_note done=<id> closes one)\n"
	switch {
	case err != nil:
		return h + "unreadable: " + err.Error() + "\n\n"
	case !ok:
		return ""
	}
	var b strings.Builder
	b.WriteString(h)
	if len(mine) == 0 {
		b.WriteString("none\n")
	}
	ranked, dups := rankItems(mine, time.Now())
	lines := itemLines(ranked, dups, true)
	for n, l := range lines {
		if b.Len()+len(l)+1 > briefTodoMax-160 {
			b.WriteString(cutLine(ranked[n:], len(mine)-len(ranked)))
			break
		}
		b.WriteString(l + "\n")
	}
	var qs []string
	for name, n := range queues {
		if s.F.OwnerOf(name) == s.Me() {
			qs = append(qs, fmt.Sprintf("%s %d", name, n))
		}
	}
	if len(qs) > 0 {
		slices.Sort(qs)
		b.WriteString("agent queues (fleet_info NAME lists one): " + strings.Join(qs, ", ") + "\n")
	}
	return b.String() + "\n"
}

// cutLine counts the items the brief cut, by kind, and names where the full
// list is.
func cutLine(cut []todo.Item, dups int) string {
	n := map[string]int{}
	for _, it := range cut {
		n[it.Kind]++
	}
	var parts []string
	for _, k := range []string{todo.Ask, todo.Promise, todo.Request, todo.Followup} {
		if n[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %ss", n[k], k))
		}
	}
	l := fmt.Sprintf("(%d more not shown: %s", len(cut), strings.Join(parts, ", "))
	if dups > 0 {
		l += fmt.Sprintf("; %d duplicates folded", dups)
	}
	return l + "; fleet_brief file=todo lists every one)\n"
}

// todos is the admiral's pending items and every agent's queue length.
func (s *Server) todos() (mine []todo.Item, queues map[string]int, ok bool, err error) {
	ok, err = fleet.TodoDo(func(c context.Context, st todo.Store) error {
		var err error
		if mine, err = st.Admiral(c, s.Me()); err != nil {
			return err
		}
		queues, err = st.Queues(c)
		return err
	})
	return mine, queues, ok, err
}

// todoLines is fleet_brief file=todo: every pending item of the admiral,
// ranked as the brief ranks them, duplicates folded.
func (s *Server) todoLines() []string {
	mine, _, ok, err := s.todos()
	switch {
	case err != nil:
		return []string{"todo store unreadable: " + err.Error()}
	case !ok:
		return []string{"no todo store in this build"}
	}
	ranked, dups := rankItems(mine, time.Now())
	return itemLines(ranked, dups, false)
}
