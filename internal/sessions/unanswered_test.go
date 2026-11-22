package sessions

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestUnanswered(t *testing.T) {
	cop := func(typ, content string) string {
		return `{"type":"` + typ + `","data":{"content":` + strings.ReplaceAll(`"`+content+`"`, "\n", `\n`) + `},"timestamp":"2026-11-22T20:41:00Z"}`
	}
	tool := `{"type":"assistant.message","data":{"content":"","toolRequests":[{"name":"fleet-fleet_handoff"}]}}`
	cla := func(typ, content string) string {
		if typ == "assistant" {
			content = `[{"type":"text","text":"` + content + `"}]`
		} else {
			content = `"` + content + `"`
		}
		return `{"type":"` + typ + `","message":{"content":` + content + `}}`
	}
	tests := []struct {
		name   string
		claude bool
		lines  []string
		want   []string
	}{
		{"copilot: queued while the admiral called fleet_handoff", false,
			[]string{cop("user.message", "whats this in dkk?"), cop("assistant.message", "Asking."), tool, cop("user.message", "still waiting for the amounts")},
			[]string{"still waiting for the amounts"}},
		{"copilot: several queued after the last step", false,
			[]string{tool, cop("user.message", "one"), cop("user.message", "two")},
			[]string{"one", "two"}},
		{"copilot: a tool call means the model saw it", false,
			[]string{cop("user.message", "one"), tool}, nil},
		{"copilot: answered", false,
			[]string{cop("user.message", "one"), cop("assistant.message", "Yes.")}, nil},
		{"claude: last prompt unanswered", true,
			[]string{cla("user", "one"), cla("assistant", "Yes."), cla("user", "two"), `{"type":"user","isMeta":true,"message":{"content":"meta"}}`},
			[]string{"two"}},
		{"claude: an interrupt marker is not a message", true,
			[]string{cla("assistant", "Yes."), cla("user", "[Request interrupted by user]")}, nil},
		{"claude: answered", true,
			[]string{cla("user", "one"), cla("assistant", "Yes.")}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path, file := dir, filepath.Join(dir, "events.jsonl")
			if tt.claude {
				path, file = filepath.Join(dir, "s.jsonl"), filepath.Join(dir, "s.jsonl")
			}
			if err := os.WriteFile(file, []byte(strings.Join(tt.lines, "\n")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := Unanswered(path); !slices.Equal(got, tt.want) {
				t.Errorf("Unanswered = %q, want %q", got, tt.want)
			}
		})
	}
}
