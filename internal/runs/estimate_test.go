package runs

import (
	"math"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
)

var t0 = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// run is a done run at effort spawned start minutes after t0 that took mins
// minutes to its first done and closed right after.
func run(effort string, start, mins float64) Record {
	c := t0.Add(time.Duration(start * float64(time.Minute)))
	d := c.Add(time.Duration(mins * float64(time.Minute)))
	return Record{Effort: effort, Created: c, FirstDone: d, Closed: d.Add(time.Second), Last: "done"}
}

func runsOf(effort string, mins ...float64) []Record {
	var rs []Record
	for i, m := range mins {
		rs = append(rs, run(effort, float64(i), m))
	}
	return rs
}

func TestEstimate(t *testing.T) {
	ten := runsOf("pro", 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	mixed := append(runsOf("light", 1, 1, 1, 1, 1, 1, 1, 1), Record{Effort: "light", Interactive: true, Created: t0, FirstDone: t0.Add(time.Hour)})
	tests := []struct {
		name   string
		past   []Record
		effort string
		want   board.Estimate
		ok     bool
	}{
		{"class", ten, "pro", board.Estimate{Basis: "pro", N: 10, P10: 1.9, P50: 5.5, P90: 9.1}, true},
		{"too few in class: table", ten[:3], "pro", board.Estimate{Basis: "table", P10: 20, P50: 27, P90: 35}, true},
		{"interactive runs left out", mixed, "light", board.Estimate{Basis: "light", N: 8, P10: 1, P50: 1, P90: 1}, true},
		{"unknown level: all runs", ten, "", board.Estimate{Basis: "all", N: 10, P10: 1.9, P50: 5.5, P90: 9.1}, true},
		{"nothing", nil, "", board.Estimate{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Estimate(tt.past, tt.effort)
			if ok != tt.ok || !near(got, tt.want) {
				t.Fatalf("got %+v %v, want %+v %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func near(a, b board.Estimate) bool {
	eq := func(x, y float64) bool { return math.Abs(x-y) < 1e-9 }
	return a.Basis == b.Basis && a.N == b.N && eq(a.P10, b.P10) && eq(a.P50, b.P50) && eq(a.P90, b.P90)
}

func TestPhases(t *testing.T) {
	r := run("pro", 0, 30)
	r.TimeS = map[string]int{"queued": 120}
	r.Merged = t0.Add(20 * time.Minute)
	r.Closed = r.FirstDone.Add(time.Minute)
	q, w, m, tail := r.Phases()
	if q != 2*time.Minute || w != 18*time.Minute || m != 10*time.Minute || tail != time.Minute {
		t.Fatalf("phases %v %v %v %v", q, w, m, tail)
	}
	r.Merged = time.Time{} // no merge seen: work runs to the first done
	if _, w, m, _ := r.Phases(); w != 28*time.Minute || m != 0 {
		t.Fatalf("no merge: work %v merged %v", w, m)
	}
}

func TestBacktestUsesOnlyEarlierRuns(t *testing.T) {
	// Eight 10-minute runs, then two 100-minute runs spawned before any of
	// them closed: their estimate may not see each other.
	rs := runsOf("standard", 10, 10, 10, 10, 10, 10, 10, 10)
	late := run("standard", 200, 100)
	rs = append(rs, late, run("standard", 201, 100))
	s := Backtest(rs, 2, Estimate)
	if s.Runs != 2 || s.Inside != 0 || s.MedianRatio != 10 || s.Within2x != 0 || s.Pinball <= 0 {
		t.Fatalf("score %+v", s)
	}
	if s := Backtest(rs, 0, Estimate); s.Runs != 10 { // runs with too little past fall back to the table
		t.Fatalf("all runs: %+v", s)
	}
}

func TestPhaseMediansCommitAndRelease(t *testing.T) {
	r := run("pro", 0, 30)
	r.Repo, r.TimeS = "fleet", map[string]int{"queued": 60}
	r.FirstCommit = t0.Add(11 * time.Minute)
	r.Merged = t0.Add(20 * time.Minute)
	other := r
	other.Repo = "elsewhere"                    // a release of this repo did not ship it
	other.Merged = r.FirstDone.Add(time.Minute) // merged after its done: no merge-to-done phase
	releases := []time.Time{t0.Add(10 * time.Minute), t0.Add(50 * time.Minute), t0.Add(35 * time.Minute)}
	p := Phases([]Record{r, other}, "pro", "fleet", releases)
	if p.Committed != 2 || p.FirstCommit != 10 || p.Released != 1 || p.Release != 15 || p.Merged != 1 || p.ToDone != 10 {
		t.Fatalf("phases %+v", p)
	}
}
