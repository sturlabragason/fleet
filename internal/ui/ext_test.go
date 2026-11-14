package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

type stubView struct{ open *bool }

func (v stubView) Open(*Model) bool         { return *v.open }
func (stubView) Key(*Model, string) tea.Cmd { return nil }
func (stubView) Lines(*Model) []string      { return nil }

func withExts(t *testing.T, es ...Extension) {
	oldE, oldK := exts, extKeys
	exts, extKeys = nil, map[string]func(*Model) tea.Cmd{}
	t.Cleanup(func() { exts, extKeys = oldE, oldK })
	for _, e := range es {
		Register(e)
	}
}

func TestExtensionsCompose(t *testing.T) {
	var applied []any
	aOpen, bOpen := false, false
	type msgB struct{}
	withExts(t,
		Extension{Name: "a", Poll: func(Backend) any { return "A" }, Apply: func(_ *Model, v any) { applied = append(applied, v) },
			Badge: func(*Model) string { return "[a]" }, View: stubView{&aOpen},
			Keys: map[string]func(*Model) tea.Cmd{"1x": func(*Model) tea.Cmd { return nil }}},
		Extension{Name: "b", Poll: func(Backend) any { return "B" }, Apply: func(_ *Model, v any) { applied = append(applied, v) },
			Badge: func(*Model) string { return "[b]" }, View: stubView{&bOpen},
			Update: func(_ *Model, msg tea.Msg) (tea.Cmd, bool) { _, ok := msg.(msgB); return nil, ok },
			Keys:   map[string]func(*Model) tea.Cmd{"2x": func(*Model) tea.Cmd { return nil }}},
	)
	m := &Model{}
	extApply(m, extPoll(nil))
	if len(applied) != 2 || applied[0] != "A" || applied[1] != "B" {
		t.Fatalf("applied %v", applied)
	}
	if extBadge(m) != "[a][b]" {
		t.Fatalf("badge %q", extBadge(m))
	}
	if _, ok := extUpdate(m, msgB{}); !ok {
		t.Fatal("b's message not handled")
	}
	if _, ok := extUpdate(m, 1); ok {
		t.Fatal("unknown message handled")
	}
	if extViewOpen(m) {
		t.Fatal("a view is open")
	}
	bOpen = true
	if v, ok := extView(m).(stubView); !ok || v.open != &bOpen {
		t.Fatal("b's view not the open one")
	}
	if extKeys["1x"] == nil || extKeys["2x"] == nil {
		t.Fatal("keys not merged")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("a key registered twice did not panic")
		}
	}()
	Register(Extension{Name: "c", Keys: map[string]func(*Model) tea.Cmd{"1x": nil}})
}
