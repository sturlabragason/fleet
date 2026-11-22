package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sturlabragason/fleet/internal/fleet"
)

func TestReplyPicker(t *testing.T) {
	replies := []fleet.Quotable{{Ref: "#1", Text: "Draft is ready"}, {Ref: "#2", Text: "Fix the date on the page?"}}
	resolve := func(ref string) []fleet.Quotable {
		if ref == "b5ea" {
			return []fleet.Quotable{{Ref: "todo b5ea11fd", Text: "promise: send the deck"}}
		}
		return nil
	}
	tests := []struct {
		name  string
		typed string
		down  int
		want  string // Ref picked with ⏎; "" for nothing
	}{
		{"newest by default", "", 0, "#1"},
		{"arrow down", "", 1, "#2"},
		{"words filter", "fix DATE", 0, "#2"},
		{"todo id resolves first", "b5ea", 0, "todo b5ea11fd"},
		{"nothing matches", "zzz", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Helper()
			m := newReplyModel(replies, resolve, "")
			for _, r := range tt.typed {
				m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			for range tt.down {
				m.Update(tea.KeyMsg{Type: tea.KeyDown})
			}
			m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			got := ""
			if m.chosen != nil {
				got = m.chosen.Ref
			}
			if got != tt.want {
				t.Errorf("picked %q, want %q\n%s", got, tt.want, m.View())
			}
		})
	}
}
