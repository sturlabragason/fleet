package ui

import tea "github.com/charmbracelet/bubbletea"

// Hooks for optional features compiled in with a build tag (DESIGN §15,
// "Extensions"); nil in a default build.
var (
	// extPoll reads extra state off the backend in the poll; extApply
	// stores it on the model in Update.
	extPoll  func(be Backend) any
	extApply func(m *Model, v any)
	// extUpdate handles its own message types; ok false passes msg on.
	extUpdate func(m *Model, msg tea.Msg) (cmd tea.Cmd, ok bool)
	// extView is a full view that replaces the tiles while it is open:
	// it takes every key and ignores the mouse.
	extView interface {
		Open(m *Model) bool
		Key(m *Model, k string) tea.Cmd
		Lines(m *Model) []string
	}
	// extKeys are extra overview keys.
	extKeys = map[string]func(m *Model) tea.Cmd{}
	// extBadge is extra header badge text.
	extBadge func(m *Model) string
)

func extViewOpen(m *Model) bool { return extView != nil && extView.Open(m) }
