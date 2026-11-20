package fleet

import (
	"context"
	"errors"
	"strings"

	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/todo"
)

// AddItem records one of admiral's open items (ADR-164), for fleet_note
// open= and the `fleet note --open` CLI. Its context is ctx, else the one
// the agent's dir maps to, else the configured default. ok is false
// without a store.
func (f *Fleet) AddItem(admiral, kind, text, agent, ctx string) (it todo.Item, ok bool, err error) {
	dir := ""
	if ctx == "" {
		if ag, found := f.Store.Load().Agents[agent]; found {
			dir = WorkDir(ag)
		}
	}
	ok, err = TodoDo(func(c context.Context, st todo.Store) error {
		var err error
		if ctx == "" {
			ctx = st.Context(dir)
		}
		it, err = st.Add(c, todo.Item{Admiral: admiral, Kind: kind, Ctx: ctx, Description: text, Sessions: f.sessions(admiral, agent)})
		return err
	})
	return it, ok, err
}

// DoneItem completes the open item ref (fleet_note done=, `fleet note
// --done`) and logs a "done" note from admiral, with text as the outcome.
func (f *Fleet) DoneItem(admiral, agent, ref, text string) (todo.Item, error) {
	var it todo.Item
	ok, err := TodoDo(func(c context.Context, st todo.Store) error {
		var err error
		it, err = st.Done(c, ref, f.sessions(admiral, agent)...)
		return err
	})
	if err != nil {
		return it, err
	}
	if !ok {
		return it, errors.New("done= needs a todo store, which this build does not have")
	}
	line := "done " + it.Line()
	if t := strings.TrimSpace(text); t != "" {
		line += " -- " + t
	}
	return it, supervisor.AppendNoteBy(f.Home, admiral, agent, line)
}

// sessions are the contributors among names (ADR-184), deduplicated; a
// name that is neither an agent nor an admiral is left out.
func (f *Fleet) sessions(names ...string) []todo.Contributor {
	var cs []todo.Contributor
	agents := f.Store.Load().Agents
	for _, n := range names {
		if a, ok := agents[n]; ok {
			cs = todo.Contribute(cs, todo.Contributor{Name: a.Name, Profile: a.Profile, Session: a.SessionID})
			continue
		}
		if n != "" && supervisor.Exists(f.Home, n) {
			s, _ := supervisor.LoadOf(f.Home, n)
			cs = todo.Contribute(cs, todo.Contributor{Name: n, Profile: s.Profile, Session: s.SessionID})
		}
	}
	return cs
}
