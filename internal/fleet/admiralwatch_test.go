package fleet

import (
	"strings"
	"testing"
	"time"
)

func TestAdmiralPaneExitReportedOnce(t *testing.T) {
	f, r := deadFleet(t, "sleep 60", "", "")
	pane, err := f.T.Run("new-window", "-d", "-t", Session, "-P", "-F", "#{pane_id}", "sleep 0.3; exit 3")
	if err != nil {
		t.Fatal(err)
	}
	f.T.SetPaneOption(pane, "remain-on-exit", "on")
	f.T.SetPaneOption(pane, "@role", "sup")
	f.T.SetPaneOption(pane, "@fleet_admiral", "ops")
	poll(t, f, func() bool { return len(reports(t, f)) > 0 })
	for i := 0; i < 3; i++ {
		f.Refresh()
		time.Sleep(50 * time.Millisecond)
	}
	ms := reports(t, f)
	if len(ms) != 1 || ms[0].From != "ops" || !strings.Contains(ms[0].Text, "admiral ops's pane exited (exit 3)") {
		t.Fatalf("reports %+v", ms)
	}
	if _, pasted := r.get(); len(pasted) != 1 || !strings.HasPrefix(pasted[0], "[fleet] admiral ops's pane exited") {
		t.Fatalf("wake %q", pasted)
	}
}
