package fleet

import (
	"strings"
	"testing"
)

func TestGate(t *testing.T) {
	menu := Pending{Question: "Allow write to go.mod?", Options: []string{"1. Yes", "2. No"}}
	bare := Pending{Options: []string{"1. Yes, I trust this folder", "2. No, exit"}}
	tests := []struct {
		name   string
		now    Pending
		expect string
		bad    string // "" = allowed; else a substring of the refusal
	}{
		{"same prompt", menu, "Allow write to go.mod? | 1. Yes | 2. No", ""},
		{"question changed", menu, "Allow write to main.go? | 1. Yes | 2. No", "prompt changed: now 'Allow write to go.mod? | 1. Yes | 2. No'"},
		{"options changed under the same question", menu, "Allow write to go.mod? | 1. Yes | 2. Yes, always | 3. No", "prompt changed"},
		{"question alone is not enough", menu, "Allow write to go.mod?", "prompt changed"},
		{"options only, same", bare, "1. Yes, I trust this folder | 2. No, exit", ""},
		{"options changed", bare, "1. Yes | 2. No", "prompt changed"},
		{"report", Pending{Question: "Teal or blue?", Report: "question"}, "Teal or blue?", ""},
		{"no expect", menu, "", "expect is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Gate(tt.now, tt.expect)
			switch {
			case tt.bad == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tt.bad != "" && (err == nil || !strings.Contains(err.Error(), tt.bad)):
				t.Fatalf("got %v, want a refusal with %q", err, tt.bad)
			}
		})
	}
}
