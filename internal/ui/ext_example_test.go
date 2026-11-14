//go:build extexample

package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"
)

type exampleBE struct {
	fakeBE
	n int
}

func (b *exampleBE) ExampleCount() int { return b.n }

func exampleModel(t *testing.T, be Backend) *Model {
	t.Helper()
	zone.NewGlobal()
	m := New(be, "")
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 30})
	return m
}

func TestExampleExtPollApply(t *testing.T) {
	m := exampleModel(t, &exampleBE{n: 3})
	m.Update(m.poll()())
	if m.example().count != 3 {
		t.Fatalf("count %d after poll", m.example().count)
	}
}

func TestExampleExtUpdate(t *testing.T) {
	m := exampleModel(t, &fakeBE{})
	m.Update(exampleMsg("hello"))
	if m.example().note != "hello" {
		t.Fatalf("note %q", m.example().note)
	}
}

func TestExampleExtKeyAndView(t *testing.T) {
	m := exampleModel(t, &fakeBE{})
	m.Update(exampleMsg("hello"))
	if strings.Contains(m.View(), "example view") {
		t.Fatal("view open before its key")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if v := m.View(); !strings.Contains(v, "example view") || !strings.Contains(v, "note: hello") {
		t.Fatalf("view not open after e:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if strings.Contains(m.View(), "example view") {
		t.Fatal("esc did not close the view")
	}
}

func TestExampleExtBadge(t *testing.T) {
	m := exampleModel(t, &exampleBE{n: 2})
	if strings.Contains(m.header(), "ex ") {
		t.Fatal("badge before the poll")
	}
	m.Update(m.poll()())
	if h := m.header(); !strings.Contains(h, "ex 2") {
		t.Fatalf("header lacks the badge: %q", h)
	}
}
