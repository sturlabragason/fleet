package wall

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/tmux"
)

// mainWindow starts f:main with an admiral and an overview and one hidden
// agent, as `fleet up` leaves it.
func mainWindow(t *testing.T) (c *tmux.Client, sup, ov, agent string) {
	t.Helper()
	c = &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { c.KillServer() })
	sup, err := c.Run("new-session", "-d", "-s", "f", "-n", "main", "-x", "200", "-y", "50", "-P", "-F", "#{pane_id}", "cat")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	ov, _ = c.Run("split-window", "-d", "-h", "-t", "f:main", "-P", "-F", "#{pane_id}", "cat")
	c.SetPaneOption(sup, "@role", "sup")
	c.SetPaneOption(ov, "@role", "ov")
	agent, _ = c.Spawn("f", "ag-a1", "", nil, "cat")
	c.SetPaneOption(agent, "@fleet_agent", "a1")
	return c, sup, ov, agent
}

func windowName(t *testing.T, c *tmux.Client, pane string) string {
	t.Helper()
	n, _ := c.Run("display-message", "-p", "-t", pane, "#{window_name}")
	return strings.TrimSpace(n)
}

// The incident of 2026-11-19: `fleet_wall show` with no name resolved to the
// admiral, swapped it into the target slot and renamed main after the agent;
// every later wall call failed with "can't find window: main".
func TestShowRefusesAdmiralAndHealsRenamedMain(t *testing.T) {
	c, sup, ov, agent := mainWindow(t)
	w := New(c, "f")
	if _, err := w.Show(agent); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Show(sup); err == nil || !strings.Contains(err.Error(), "not an agent pane") {
		t.Fatalf("show admiral: err %v, want a refusal", err)
	}
	if got := windowName(t, c, sup); got != "main" {
		t.Fatalf("main window renamed to %q by a refused show", got)
	}

	c.Run("rename-window", "-t", "f:main", "ag-a1")
	s, err := w.Read()
	if err != nil {
		t.Fatalf("read after rename: %v", err)
	}
	if got := windowName(t, c, ov); got != "main" || s.Ov != ov || len(s.Wall) != 1 {
		t.Fatalf("after heal: window %q, state %+v", got, s)
	}

	c.Run("kill-pane", "-t", ov)
	c.Run("rename-window", "-t", sup, "other")
	if _, err := w.Read(); err == nil || !strings.Contains(err.Error(), "no main window") || strings.Contains(err.Error(), "list-panes") {
		t.Fatalf("read with no overview: err %v, want a clear error", err)
	}
}
