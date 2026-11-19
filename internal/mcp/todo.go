package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"

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
// gets, so the list is in the handoff too.
func (s *Server) handoffTasks(items []string) string {
	var b strings.Builder
	for _, raw := range items {
		if strings.TrimSpace(raw) == "" {
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

// todoBrief is fleet_brief's "Open asks / in flight": the admiral's pending
// items, then the queues of its agents; "" without a store.
func (s *Server) todoBrief() string {
	var mine []todo.Item
	var queues map[string]int
	ok, err := fleet.TodoDo(func(c context.Context, st todo.Store) error {
		var err error
		if mine, err = st.Admiral(c, s.Me()); err != nil {
			return err
		}
		queues, err = st.Queues(c)
		return err
	})
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
	for n, it := range mine {
		l := it.Line() + "\n"
		if b.Len()+len(l) > briefTodoMax {
			fmt.Fprintf(&b, "(%d more)\n", len(mine)-n)
			break
		}
		b.WriteString(l)
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
