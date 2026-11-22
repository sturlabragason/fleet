package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/tools"
)

func TestDraftSurvivesAdmiralRestart(t *testing.T) {
	defer func(w, p, s, r time.Duration) { draftWait, draftPoll, draftSettle, draftRestoreWait = w, p, s, r }(draftWait, draftPoll, draftSettle, draftRestoreWait)
	draftWait, draftPoll, draftSettle, draftRestoreWait = 50*time.Millisecond, time.Millisecond, 20*time.Millisecond, 50*time.Millisecond
	tests := []struct {
		name        string
		boxes       []string // what the old admiral's box shows on each look; the last repeats
		busy        bool     // the old admiral turns busy once its box is empty (the user pressed Enter)
		wantDraft   string
		wantPending bool
		wantSent    bool
		wantTyped   string
	}{
		{"empty box: no wait, nothing kept", []string{""}, false, "", false, false, ""},
		{"fleet's own wake line is not the user's", []string{"[fleet] report from a1"}, false, "", false, false, ""},
		{"user clears while held: saved, not retyped, not sent on", []string{"deploy it", "deploy it now", ""}, false, "deploy it now", false, false, ""},
		{"user submits while held: handed to the new admiral", []string{"deploy it", "deploy it now", ""}, true, "deploy it now", false, true, ""},
		{"still typed at the deadline: moved to the new admiral", []string{"half a thought"}, false, "half a thought", true, false, "half a thought"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := testFleet(t)
			i := 0
			f.SupPane = func() fleet.AdmiralView {
				in := tt.boxes[min(i, len(tt.boxes)-1)]
				i++
				return fleet.AdmiralView{Pane: "%sup", Box: true, Input: in, Idle: in != "" || !tt.busy}
			}
			d := holdForDraft(f, supervisor.Main)
			if d.Text != tt.wantDraft || d.Pending != tt.wantPending || d.Sent != tt.wantSent {
				t.Fatalf("hold = %+v; want %q, pending %v, sent %v", d, tt.wantDraft, tt.wantPending, tt.wantSent)
			}
			boot := bootPrompt(d)
			if carried := boot != supervisor.BootPrompt; carried != tt.wantSent || !strings.HasPrefix(boot, supervisor.BootPrompt) || (carried && !strings.HasSuffix(boot, tt.wantDraft)) {
				t.Errorf("boot prompt %q: carries the text = %v, want %v", boot, carried, tt.wantSent)
			}
			// The new admiral's box starts empty.
			f.SupPane = func() fleet.AdmiralView { return fleet.AdmiralView{Pane: "%sup", Box: true} }
			typed := ""
			f.TypeText = func(_, text string) error { typed = text; return nil }
			restoreDraft(f, supervisor.Main, d)
			if typed != tt.wantTyped {
				t.Errorf("typed %q, want %q", typed, tt.wantTyped)
			}
			saved, _ := filepath.Glob(filepath.Join(f.Home, "drafts", supervisor.Main+"-*.txt"))
			if tt.wantDraft == "" {
				if len(saved) != 0 {
					t.Errorf("saved %v for an empty box", saved)
				}
				return
			}
			if len(saved) != 1 {
				t.Fatalf("saved %v", saved)
			}
			if b, _ := os.ReadFile(saved[0]); string(b) != tt.wantDraft+"\n" {
				t.Errorf("saved %q", b)
			}
		})
	}
}

func TestBootPromptCarriesUnanswered(t *testing.T) {
	tests := []struct {
		name  string
		d     draft
		want  []string
		count string // text that must appear exactly once
	}{
		{"nothing to carry", draft{}, nil, ""},
		{"queued message the old admiral never answered", draft{Unanswered: []string{"still waiting for the amounts\nall at 1150"}},
			[]string{"## Last user messages", "> still waiting for the amounts\n> all at 1150"}, "still waiting"},
		{"sent during the hold and also in the session: once", draft{Text: "deploy it no", Sent: true, Unanswered: []string{"deploy it now"}},
			[]string{"> deploy it now"}, "deploy it no"},
		{"sent during the hold, not in the session yet", draft{Text: "deploy it", Sent: true, Unanswered: []string{"earlier"}},
			[]string{"> earlier", "never answered", "deploy it"}, "deploy it"},
		{"sent during the hold, an older message shares its start", draft{Text: "ok", Sent: true, Unanswered: []string{"ok but check first", "later"}},
			[]string{"Answer it first:\n\nok"}, ""},
		{"too long for the launch command: named file", draft{Unanswered: []string{"long"}, UnansweredFile: "/x/main-unanswered.md"},
			[]string{"## Last user messages", "They are in /x/main-unanswered.md"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			boot := bootPrompt(tt.d)
			if !strings.HasPrefix(boot, supervisor.BootPrompt) {
				t.Fatalf("boot prompt %q lost the boot text", boot)
			}
			if tt.want == nil && boot != supervisor.BootPrompt {
				t.Errorf("boot prompt %q carries something", boot)
			}
			for _, w := range tt.want {
				if !strings.Contains(boot, w) {
					t.Errorf("boot prompt %q lacks %q", boot, w)
				}
			}
			if tt.count != "" && strings.Count(boot, tt.count) != 1 {
				t.Errorf("boot prompt %q has %q %d times", boot, tt.count, strings.Count(boot, tt.count))
			}
		})
	}
}

func TestWithUnanswered(t *testing.T) {
	long := strings.Repeat("it's ", 100)
	tests := []struct {
		name     string
		msgs     []string
		wantN    int
		wantFile bool
	}{
		{"short: inline", []string{"still waiting for the amounts"}, 1, false},
		{"fleet's own lines are not carried", []string{"[fleet] report from a1", supervisor.BootPrompt}, 0, false},
		{"too long for tmux: in a file", []string{long}, 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := testFleet(t)
			store := t.TempDir()
			f.Profiles = map[string]tools.Profile{"copilot": {Name: "copilot", SessionPath: store + "/{id}"}}
			sess := filepath.Join(store, "0123456789abcdef")
			os.MkdirAll(sess, 0o700)
			ev := `{"type":"assistant.message","data":{"content":"ok"}}` + "\n"
			for _, m := range tt.msgs {
				b, _ := json.Marshal(map[string]any{"type": "user.message", "data": map[string]string{"content": m}})
				ev += string(b) + "\n"
			}
			os.WriteFile(filepath.Join(sess, "events.jsonl"), []byte(ev), 0o600)
			d := withUnanswered(f, supervisor.Main, "0123456789abcdef", draft{})
			if len(d.Unanswered) != tt.wantN || (d.UnansweredFile != "") != tt.wantFile {
				t.Fatalf("got %d messages, file %q; want %d, file %v", len(d.Unanswered), d.UnansweredFile, tt.wantN, tt.wantFile)
			}
			boot := bootPrompt(d)
			if len(boot)+16*strings.Count(boot, "'") > len(supervisor.BootPrompt)+maxInline+2 {
				t.Errorf("boot prompt is %d bytes, too long for the launch command", len(boot))
			}
			if tt.wantFile {
				if b, _ := os.ReadFile(d.UnansweredFile); !strings.Contains(string(b), "it's") {
					t.Errorf("file %s lacks the message", d.UnansweredFile)
				}
			}
		})
	}
}
