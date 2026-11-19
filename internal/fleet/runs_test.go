package fleet

import (
	"testing"

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
