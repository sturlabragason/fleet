package fleet

import (
	"strings"
	"testing"
	"time"
)

// Putting a parked agent on the wall wakes it first (#45), under the memory
// guard; a refusal comes back as an error that says why.
func TestPaneOrWakeWakesAParkedAgent(t *testing.T) {
	f, _ := parkFleet(t, "[memory]\nmin_free = \"4GB\"\n")
	free := uint64(8 << 30)
	f.FreeMem = func() (uint64, error) { return free, nil }
	if _, err := f.Spawn("w1", "res", f.Home, ""); err != nil {
		t.Fatal(err)
	}
	waitPane(t, f, "w1")
	setState(t, f, "w1", "idle", time.Now())
	if err := f.Park("w1", ""); err != nil {
		t.Fatal(err)
	}

	free = 1 << 30
	if _, woke, err := f.PaneOrWake("w1"); err == nil || woke || !strings.Contains(err.Error(), "not waking w1") {
		t.Fatalf("low memory: woke=%v err=%v", woke, err)
	}
	if st := f.Store.Load().Agents["w1"].Status; st != "parked" {
		t.Fatalf("refused wake left it %s", st)
	}

	free = 8 << 30
	pane, woke, err := f.PaneOrWake("w1")
	if err != nil || !woke || pane == "" || pane != f.paneOf("w1") {
		t.Fatalf("pane=%q woke=%v err=%v (live pane %q)", pane, woke, err, f.paneOf("w1"))
	}
	again, woke, err := f.PaneOrWake("w1")
	if err != nil || woke || again != pane {
		t.Fatalf("running agent: pane=%q woke=%v err=%v", again, woke, err)
	}
	if _, _, err := f.PaneOrWake("nobody"); err == nil {
		t.Fatal("unknown agent must fail")
	}
}
