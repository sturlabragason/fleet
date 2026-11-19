package runs

import (
	"testing"
	"time"
)

func TestAppendReadSince(t *testing.T) {
	home := t.TempDir()
	t0 := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for i, n := range []string{"old", "new"} {
		if err := Append(home, Record{Name: n, Closed: t0.Add(time.Duration(i) * 48 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	rs, err := Read(home, t0.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Name != "new" {
		t.Fatalf("got %+v", rs)
	}
	if rs, err := Read(t.TempDir(), time.Time{}); err != nil || rs != nil {
		t.Fatalf("missing ledger: %v %v", rs, err)
	}
}

func TestSummarize(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	done := func(p string, mins int) Record {
		return Record{Profile: p, Last: "done", Created: t0, FirstDone: t0.Add(time.Duration(mins) * time.Minute),
			Reports: map[string]int{"done": 1}, Spend: &Spend{AIC: 100, Compactions: 1}}
	}
	rework := done("x", 30)
	rework.Reports["done"] = 2
	checked := done("x", 20)
	checked.CheckResult = "fail"
	rs := []Record{done("x", 10), rework, checked,
		{Profile: "x", Last: "blocked", Interventions: map[string]int{"stuck_wake": 2}},
		{Profile: "x", Interactive: true},
		{Profile: ""}}
	rows := Summarize(rs, Keys["profile"])
	if len(rows) != 2 || rows[0].Key != "x" || rows[1].Key != "-" {
		t.Fatalf("rows %+v", rows)
	}
	x := rows[0]
	want := Row{Key: "x", Runs: 4, Done: 3, Checked: 1, Passed: 0, Rework: 1, MedianMinutes: 20,
		AIC: 300, Compactions: 3, Interventions: 2, Few: true}
	x.DoneLo, x.DoneHi = 0, 0
	if x != want {
		t.Fatalf("got %+v\nwant %+v", x, want)
	}
	lo, hi := wilson(3, 4)
	if lo < 0.29 || lo > 0.31 || hi < 0.95 || hi > 0.96 {
		t.Fatalf("wilson(3,4) = %.3f-%.3f", lo, hi)
	}
}
