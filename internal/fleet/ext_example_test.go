//go:build extexample

package fleet

import (
	"testing"
	"time"
)

func TestExampleExtWake(t *testing.T) {
	f, _ := mailFleet(t)
	st := exampleOn(f)
	f.DeliverWake()
	if n := st.wakes.Load(); n != 1 {
		t.Fatalf("example Wake ran %d times, want 1", n)
	}
}

func TestExampleExtAdmiralBadge(t *testing.T) {
	f, _ := mailFleet(t)
	exampleOn(f).badge = map[string]string{"main": "ex 2"}
	pane, err := f.T.Spawn(Session, "main", f.Home, nil, "sleep 60")
	if err != nil {
		t.Fatal(err)
	}
	f.T.SetPaneOption(pane, "@role", "sup")
	f.syncAdmiralPanes()
	for i := 0; i < 20 && f.T.PaneOption(pane, "@fleet_admiral_ext") == ""; i++ {
		time.Sleep(25 * time.Millisecond)
	}
	if got := f.T.PaneOption(pane, "@fleet_admiral_ext"); got != "ex 2" {
		t.Fatalf("@fleet_admiral_ext = %q, want %q", got, "ex 2")
	}
}
