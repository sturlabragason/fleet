package ui

import (
	"maps"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Extension is what a feature compiled in with a build tag adds to the
// overview (DESIGN §15 "Extensions", ADR-126). It calls Register from its
// own init(); a default build registers nothing.
type Extension struct {
	Name string
	// Poll reads extra state off the backend in the poll; Apply gets that
	// value back on the model in Update.
	Poll  func(be Backend) any
	Apply func(m *Model, v any)
	// Update handles its own message types; ok false passes msg on.
	Update func(m *Model, msg tea.Msg) (cmd tea.Cmd, ok bool)
	// View is a full view that replaces the tiles while it is open: it takes
	// every key and ignores the mouse.
	View ExtView
	// Keys are extra overview keys.
	Keys map[string]func(m *Model) tea.Cmd
	// Badge is extra header badge text.
	Badge func(m *Model) string
}

// ExtView is an extension's full-screen view.
type ExtView interface {
	Open(m *Model) bool
	Key(m *Model, k string) tea.Cmd
	Lines(m *Model) []string
}

var (
	exts    []Extension
	extKeys = map[string]func(m *Model) tea.Cmd{}
)

// Register adds an extension; every registered extension's hooks run, in
// registration order. Call it from init(). A name or key registered twice
// panics.
func Register(e Extension) {
	if e.Name == "" {
		panic("ui.Register: extension has no name")
	}
	for _, x := range exts {
		if x.Name == e.Name {
			panic("ui.Register: extension " + e.Name + " registered twice")
		}
	}
	for k := range e.Keys {
		if extKeys[k] != nil {
			panic("ui.Register: overview key " + k + " registered twice")
		}
	}
	exts = append(exts, e)
	maps.Copy(extKeys, e.Keys)
}

// extPoll is each extension's poll result, by its index in exts.
func extPoll(be Backend) []any {
	if len(exts) == 0 {
		return nil
	}
	out := make([]any, len(exts))
	for i, e := range exts {
		if e.Poll != nil {
			out[i] = e.Poll(be)
		}
	}
	return out
}

func extApply(m *Model, vs []any) {
	for i, e := range exts {
		if e.Apply != nil && i < len(vs) {
			e.Apply(m, vs[i])
		}
	}
}

// extUpdate offers msg to each extension; the first that handles it wins.
func extUpdate(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	for _, e := range exts {
		if e.Update != nil {
			if c, ok := e.Update(m, msg); ok {
				return c, true
			}
		}
	}
	return nil, false
}

// extView is the first open extension view, nil when none is.
func extView(m *Model) ExtView {
	for _, e := range exts {
		if e.View != nil && e.View.Open(m) {
			return e.View
		}
	}
	return nil
}

func extViewOpen(m *Model) bool { return extView(m) != nil }

func extBadge(m *Model) string {
	var s strings.Builder
	for _, e := range exts {
		if e.Badge != nil {
			s.WriteString(e.Badge(m))
		}
	}
	return s.String()
}
