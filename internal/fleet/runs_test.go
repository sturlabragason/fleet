package fleet

import (
	"reflect"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"

	"github.com/sturlabragason/fleet/internal/runs"
)

func TestCloseAppendsRunToLedger(t *testing.T) {
	f, _ := mailFleet(t)
	for _, st := range []string{"progress", "done", "done"} {
		if _, err := f.Report("a", st, "step"); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close("a"); err != nil {
		t.Fatal(err)
	}
	rs, err := runs.Read(f.Home, f.Store.Load().Agents["b"].Created)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("ledger has %d runs, want 1: %+v", len(rs), rs)
	}
	r := rs[0]
	if r.Name != "a" || r.Profile != "cat" || r.Driver != "tmux" || r.Last != "done" || r.FirstDone.IsZero() {
		t.Fatalf("record %+v", r)
	}
	if r.Reports["progress"] != 1 || r.Reports["done"] != 2 {
		t.Fatalf("reports %v", r.Reports)
	}
	if r.Config == nil {
		t.Fatal("no config fingerprint recorded at spawn")
	}
}

// ADR-187: one pass over the events fills time in each status and the
// friction counters; other agents' lines and crash events don't count.
func TestTallyEvents(t *testing.T) {
	t0 := time.Now()
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	evs := []board.Event{
		{Time: at(0), Agent: "a", To: "working"},
		{Time: at(5), Agent: "b", To: "idle"},
		{Time: at(30), Agent: "a", From: "working", To: "waiting"},
		{Time: at(31), Agent: "a", Kind: board.KindDeliver, Detail: "steered"},
		{Time: at(32), Agent: "a", Kind: board.KindDeliver, Detail: "user_pane"},
		{Time: at(33), Agent: "a", Kind: board.KindTakeover, Detail: "to_tmux"},
		{Time: at(34), Agent: "a", Kind: board.KindDoneRefused, Detail: "unmerged"},
		{Time: at(35), Agent: "a", Kind: board.KindCrash, Detail: "exit=1"},
		{Time: at(36), Agent: "a", Kind: board.KindStuckWake},
		{Time: at(40), Agent: "a", From: "waiting", To: "working"},
		{Time: at(41), Agent: "a", Kind: board.KindDoneCheck, Detail: "pass"},
		{Time: at(42), Agent: "a", Kind: board.KindMerge},
		{Time: at(43), Agent: "a", Kind: board.KindMerge}, // a second merge keeps the first time
	}
	rec := runs.Record{Name: "a"}
	tallyEvents(&rec, evs, at(100))
	want := runs.Record{Name: "a", CheckResult: "pass", DoneBounces: 1, Merged: at(42),
		TimeS:         map[string]int{"working": 90, "waiting": 10},
		Sends:         map[string]int{"steered": 1, "user_pane": 1, "takeover": 1},
		DoneRefusals:  map[string]int{"unmerged": 1},
		Interventions: map[string]int{board.KindStuckWake: 1}}
	if !reflect.DeepEqual(rec, want) {
		t.Fatalf("got  %+v\nwant %+v", rec, want)
	}
}

// An escalation to the admiral is a refused done but not a bounce.
func TestTallyEventsEscalationIsNoBounce(t *testing.T) {
	rec := runs.Record{Name: "a"}
	tallyEvents(&rec, []board.Event{{Agent: "a", Kind: board.KindDoneRefused, Detail: "no_merge_perm"}}, time.Now())
	if rec.DoneBounces != 0 || rec.DoneRefusals["no_merge_perm"] != 1 {
		t.Fatalf("record %+v", rec)
	}
}

func TestOldestOwn(t *testing.T) {
	tests := []struct{ name, log, want string }{
		{"never rebased: every commit", "2026-11-26T10:05:00Z\t\n2026-11-26T10:02:00Z\t\n", "2026-11-26T10:02:00Z"},
		{"rebased: only its own", "2026-11-26T10:09:00Z\ta\n2026-11-26T09:00:00Z\tb\n2026-11-26T10:07:00Z\ta\n", "2026-11-26T10:07:00Z"},
		{"nothing", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := oldestOwn(tt.log, "a")
			if (tt.want == "" && !got.IsZero()) || (tt.want != "" && got.Format(time.RFC3339) != tt.want) {
				t.Fatalf("got %v, want %q", got, tt.want)
			}
		})
	}
}
