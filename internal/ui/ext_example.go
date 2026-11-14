//go:build extexample

package ui

import (
	"fmt"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// The public example extension (DESIGN §15 "Extensions"): it fills every
// seam of this package so public CI compiles and tests them. Its state lives
// beside the Model, keyed by the pointer, as a real extension's does; it
// shows nothing until its key is pressed or a backend reports a count.
type exampleState struct {
	count int    // from the poll
	note  string // from exampleMsg
	open  bool   // e: the example view replaces the tiles
}

var exampleStates sync.Map // *Model -> *exampleState

func (m *Model) example() *exampleState {
	v, _ := exampleStates.LoadOrStore(m, &exampleState{})
	return v.(*exampleState)
}

// exampleBackend is the optional backend method the example polls.
type exampleBackend interface{ ExampleCount() int }

type exampleMsg string

type exampleView struct{}

func (exampleView) Open(m *Model) bool { return m.example().open }
func (exampleView) Key(m *Model, k string) tea.Cmd {
	if k == "esc" || k == "q" || k == "e" {
		m.example().open = false
	}
	return nil
}
func (exampleView) Lines(m *Model) []string {
	return []string{"example view", "note: " + m.example().note, "esc closes"}
}

func init() {
	Register(Extension{
		Name: "example",
		Poll: func(be Backend) any {
			if eb, ok := be.(exampleBackend); ok {
				return eb.ExampleCount()
			}
			return 0
		},
		Apply: func(m *Model, v any) {
			if n, ok := v.(int); ok {
				m.example().count = n
			}
		},
		Update: func(m *Model, msg tea.Msg) (tea.Cmd, bool) {
			if s, ok := msg.(exampleMsg); ok {
				m.example().note = string(s)
				return nil, true
			}
			return nil, false
		},
		View: exampleView{},
		Keys: map[string]func(m *Model) tea.Cmd{
			"e": func(m *Model) tea.Cmd { m.example().open = true; return nil },
		},
		Badge: func(m *Model) string {
			if n := m.example().count; n > 0 {
				return fmt.Sprintf("  ex %d", n)
			}
			return ""
		},
	})
}
