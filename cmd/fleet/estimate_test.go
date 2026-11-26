package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/runs"
)

func TestEstimateCommandOutput(t *testing.T) {
	home := t.TempDir()
	if _, ok := checkEstimates(home); ok {
		t.Fatal("doctor line with no ledger")
	}
	t0 := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	var rs []runs.Record
	for i := range 12 {
		c := t0.Add(time.Duration(i) * time.Hour)
		r := runs.Record{Name: "r", Effort: "pro", Created: c, FirstDone: c.Add(25 * time.Minute), Closed: c.Add(26 * time.Minute)}
		rs = append(rs, r)
		if err := runs.Append(home, r); err != nil {
			t.Fatal(err)
		}
	}
	c, ok := checkEstimates(home)
	if !ok || !strings.HasPrefix(c.Detail, "100% of the latest 12 done runs") {
		t.Fatalf("doctor %+v", c)
	}
	var b bytes.Buffer
	if err := writeEstimates(&b, rs, []string{"pro", "nosuch"}, "", nil, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pro: estimate p50 25 min, p90 25 min (12 past pro runs)", "median phases of 12 done runs", "nosuch: estimate p50 25 min, p90 25 min (12 past runs of every level)"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in\n%s", want, b.String())
		}
	}
}
