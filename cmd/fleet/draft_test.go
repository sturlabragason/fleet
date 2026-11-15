package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/supervisor"
)

func TestDraftSurvivesAdmiralRestart(t *testing.T) {
	defer func(w, p, r time.Duration) { draftWait, draftPoll, draftRestoreWait = w, p, r }(draftWait, draftPoll, draftRestoreWait)
	draftWait, draftPoll, draftRestoreWait = 50*time.Millisecond, time.Millisecond, 50*time.Millisecond
	tests := []struct {
		name        string
		boxes       []string // what the old admiral's box shows on each look; the last repeats
		wantDraft   string
		wantPending bool
		wantTyped   string
	}{
		{"empty box: no wait, nothing kept", []string{""}, "", false, ""},
		{"fleet's own wake line is not the user's", []string{"[fleet] report from a1"}, "", false, ""},
		{"user submits while held: saved, not retyped", []string{"deploy it", "deploy it now", ""}, "deploy it now", false, ""},
		{"still typed at the deadline: moved to the new admiral", []string{"half a thought"}, "half a thought", true, "half a thought"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := testFleet(t)
			i := 0
			f.SupPane = func() fleet.AdmiralView {
				in := tt.boxes[min(i, len(tt.boxes)-1)]
				i++
				return fleet.AdmiralView{Pane: "%sup", Box: true, Input: in}
			}
			draft, pending := holdForDraft(f, supervisor.Main)
			if draft != tt.wantDraft || pending != tt.wantPending {
				t.Fatalf("hold = %q, %v; want %q, %v", draft, pending, tt.wantDraft, tt.wantPending)
			}
			// The new admiral's box starts empty.
			f.SupPane = func() fleet.AdmiralView { return fleet.AdmiralView{Pane: "%sup", Box: true} }
			typed := ""
			f.TypeText = func(_, text string) error { typed = text; return nil }
			restoreDraft(f, supervisor.Main, draft, pending)
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
