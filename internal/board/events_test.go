package board

import (
	"bufio"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestEventKinds(t *testing.T) {
	s := NewStore(t.TempDir())
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	a := &Agent{Name: "a1", Profile: "copilot", Model: "m1"}
	tests := []struct {
		kind, detail string
		pattern      *PatternHit
	}{
		{KindStopBlock, "", nil},
		{KindNoReportWake, "", nil},
		{KindStuckWake, "grinding", nil},
		{KindRecycleWake, "", nil},
		{KindResurfaceWake, "question", nil},
		{KindSendRefused, "user_typing", nil},
		{KindConfirmRefused, "answer", nil},
		{KindModelRefused, "", nil},
		{KindDoneCheck, "pass", nil},
		{KindContextWarning, "72%", nil},
		{KindStatusDisagree, "hook=idle screen=working", &PatternHit{Set: "copilot", Kind: "working", Index: 2}},
	}
	if len(tests) != len(Kinds) {
		t.Fatalf("table has %d kinds, Kinds has %d", len(tests), len(Kinds))
	}
	for _, tc := range tests {
		e := Intervention(now, a, tc.kind, tc.detail)
		e.Pattern = tc.pattern
		s.AppendEvent(e)
	}
	s.AppendEvent(Event{Time: now, Agent: "a1", From: "idle", To: "working"})
	got, err := s.ReadEvents(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(tests)+1 {
		t.Fatalf("read %d events, want %d", len(got), len(tests)+1)
	}
	for i, tc := range tests {
		e := got[i]
		if e.Kind != tc.kind || e.Detail != tc.detail || e.Agent != "a1" || e.Profile != "copilot" || e.Model != "m1" || !e.Time.Equal(now) {
			t.Errorf("line %d = %+v, want kind %s detail %q", i, e, tc.kind, tc.detail)
		}
		if (e.Pattern == nil) != (tc.pattern == nil) || e.Pattern != nil && *e.Pattern != *tc.pattern {
			t.Errorf("line %d pattern = %+v, want %+v", i, e.Pattern, tc.pattern)
		}
	}
	if tr := got[len(tests)]; tr.Kind != "" || tr.From != "idle" || tr.To != "working" {
		t.Errorf("transition = %+v", tr)
	}
}

func TestEventRotate(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	now := time.Now()
	s.AppendEvent(Event{Time: now.Add(-2 * EventKeep), Agent: "old", From: "idle", To: "working"})
	if rotated, err := s.RotateEvents(now.Add(-3 * EventKeep)); err != nil || rotated {
		t.Fatalf("rotated a file younger than the cut: %v %v", rotated, err)
	}
	const n = 2000
	half := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range n {
			if i == n/2 {
				close(half)
			}
			s.AppendEvent(Intervention(now, &Agent{Name: "a1"}, KindStopBlock, ""))
		}
	})
	<-half
	rotated, err := s.RotateEvents(now.Add(-EventKeep))
	if err != nil || !rotated {
		t.Fatalf("RotateEvents = %v, %v; want a rotation", rotated, err)
	}
	wg.Wait()
	total := 0
	for _, name := range []string{EventsFile, EventsFile + ".1"} {
		total += countLines(t, filepath.Join(dir, name))
	}
	if total != n+1 {
		t.Fatalf("%d lines across both files, want %d", total, n+1)
	}
	got, err := s.ReadEvents(time.Time{})
	if err != nil || len(got) != n+1 {
		t.Fatalf("ReadEvents = %d events, %v; want %d", len(got), err, n+1)
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n := 0
	for sc := bufio.NewScanner(f); sc.Scan(); {
		n++
	}
	return n
}
