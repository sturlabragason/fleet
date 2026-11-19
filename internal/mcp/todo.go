package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/todo"
)

// The admiral's promises, open asks and the user's requests in flight are
// items in the todo store when the build has one (ADR-164), so a fresh admiral
// finds them in fleet_brief whatever the handoff says.

// briefTodoMax bounds the brief's open-items section.
const briefTodoMax = 2 << 10

func checkOpen(kind string) error {
	if kind != "" && !slices.Contains(todo.AdmiralKinds, kind) {
		return fmt.Errorf("open must be one of %s", strings.Join(todo.AdmiralKinds, ", "))
	}
	return nil
}

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
	it, ok, err := s.addTask(kind, text, a.Agent, a.Ctx)
	switch {
	case err != nil:
		return "; not added to the todo store: " + err.Error()
	case !ok:
		return "; no todo store in this build, so it is in the notes only"
	}
	return fmt.Sprintf("; open task [%s] (fleet_note done=%s closes it)", it.ID(), it.ID())
}

// addTask records one admiral item. Its context is ctx, else the one the
// agent's dir maps to, else the configured default.
func (s *Server) addTask(kind, text, agent, ctx string) (it todo.Item, ok bool, err error) {
	dir := ""
	if ctx == "" {
		if ag, found := s.F.Store.Load().Agents[agent]; found {
			dir = fleet.WorkDir(ag)
		}
	}
	ok, err = fleet.TodoDo(func(c context.Context, st todo.Store) error {
		var err error
		if ctx == "" {
			ctx = st.Context(dir)
		}
		it, err = st.Add(c, todo.Item{Admiral: s.Me(), Kind: kind, Ctx: ctx, Description: text})
		return err
	})
	return it, ok, err
}

// noteDone is fleet_note done=<id>: the item is completed and the decisions
// log gets a line saying so (with text, if any, as the outcome).
func (s *Server) noteDone(a args) (string, error) {
	var it todo.Item
	ok, err := fleet.TodoDo(func(c context.Context, st todo.Store) error {
		var err error
		it, err = st.Done(c, a.Done)
		return err
	})
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("done= needs a todo store, which this build does not have")
	}
	line := "done " + it.Line()
	if t := strings.TrimSpace(a.Text); t != "" {
		line += " -- " + t
	}
	if err := supervisor.AppendNoteBy(s.F.Home, s.Me(), a.Agent, line); err != nil {
		return "", err
	}
	return "closed task [" + it.ID() + "] " + it.Description, nil
}

// handoffTasks records fleet_handoff's open_items ("ask: …", "promise: …",
// "request: …"; no prefix is a request) and returns the lines the handoff
// gets, so the list is in the handoff too.
func (s *Server) handoffTasks(items []string) (string, error) {
	var b strings.Builder
	for _, raw := range items {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		kind, text := todo.Kind(raw)
		if kind == "" {
			kind = todo.Request
		}
		it, ok, err := s.addTask(kind, text, "", "")
		switch {
		case err != nil:
			return "", fmt.Errorf("open item %q: %w", text, err)
		case ok:
			b.WriteString("\n- " + it.Line())
		default:
			b.WriteString("\n- " + kind + ": " + text)
		}
	}
	if b.Len() == 0 {
		return "", nil
	}
	return "\n\nOpen items:" + b.String(), nil
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
