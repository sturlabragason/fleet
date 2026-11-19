package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/supervisor"
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
