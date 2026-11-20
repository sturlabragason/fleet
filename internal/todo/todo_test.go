package todo

import (
	"slices"
	"testing"
)

// ADR-184: an item's sessions are deduplicated by name and session, and a
// name seen before its tool told fleet the session id gets the id filled in.
func TestContribute(t *testing.T) {
	a := Contributor{Name: "a", Profile: "copilot", Session: "11111111-aaaa"}
	a2 := Contributor{Name: "a", Profile: "copilot", Session: "22222222-bbbb"}
	bare := Contributor{Name: "a", Profile: "copilot"}
	b := Contributor{Name: "b"}
	for _, tc := range []struct {
		name string
		in   []Contributor
		add  Contributor
		want []Contributor
	}{
		{"first", nil, a, []Contributor{a}},
		{"same again", []Contributor{a}, a, []Contributor{a}},
		{"no name", []Contributor{a}, Contributor{Session: "x"}, []Contributor{a}},
		{"id filled in", []Contributor{bare, b}, a, []Contributor{a, b}},
		{"no id after one", []Contributor{a}, bare, []Contributor{a}},
		{"resumed in a new session", []Contributor{a}, a2, []Contributor{a, a2}},
		{"another name", []Contributor{a}, b, []Contributor{a, b}},
	} {
		if got := Contribute(slices.Clone(tc.in), tc.add); !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestLineSessions(t *testing.T) {
	it := Item{UUID: "0123456789ab", Kind: Ask, Description: "which repo?"}
	if got, want := it.Line(), "[01234567] ask: which repo?"; got != want {
		t.Errorf("no sessions: %q, want %q", got, want)
	}
	it.Sessions = []Contributor{{Name: "main", Profile: "copilot", Session: "86f75789-5d05"}, {Name: "w"}}
	if got, want := it.Line(), "[01234567] ask: which repo?; sessions: main (copilot 86f75789), w"; got != want {
		t.Errorf("sessions: %q, want %q", got, want)
	}
}
