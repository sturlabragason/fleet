package supervisor

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/sturlabragason/fleet/internal/tmux"
)

func TestAdmiralRegistry(t *testing.T) {
	home := t.TempDir()
	for _, bad := range []string{"main", "", "Bad Name", "admiral.x"} {
		if ValidName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	a, err := Add(home, "print", "", 2)
	if err != nil || a.Slot != 1 {
		t.Fatalf("add: %+v %v", a, err)
	}
	if _, err := Add(home, "print", "", 2); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := Add(home, "docs", "", 2); err == nil {
		t.Fatal("max not enforced (main counts)")
	}
	if err := Remove(home, "print"); err != nil {
		t.Fatal(err)
	}
	if b, err := Add(home, "docs", "", 2); err != nil || b.Slot != 1 {
		t.Fatalf("slot not reused: %+v %v", b, err)
	}
}

func TestFindPaneOfHiddenAdmiral(t *testing.T) {
	c := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { c.KillServer() })
	main, err := c.Run("new-session", "-d", "-s", "f", "-n", "main", "-x", "200", "-y", "50", "-P", "-F", "#{pane_id}", "cat")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	c.SetPaneOption(main, "@role", "sup")
	hid, _ := c.Spawn("f", "sup-hidden", "", nil, "cat")
	c.SetPaneOption(hid, "@role", "sup")
	c.SetPaneOption(hid, "@fleet_admiral", "print")
	// Another session's admiral pane must never match.
	other, _ := c.Spawn("g", "main", "", nil, "cat")
	c.SetPaneOption(other, "@role", "sup")
	c.SetPaneOption(other, "@fleet_admiral", "docs")

	if got, err := FindPaneOf(c, "f:main", "print"); err != nil || got != hid {
		t.Fatalf("hidden admiral: %q %v, want %q", got, err, hid)
	}
	if got, err := FindPaneOf(c, "f:main", "main"); err != nil || got != main {
		t.Fatalf("main: %q %v", got, err)
	}
	if _, err := FindPaneOf(c, "f:main", "docs"); err != ErrNoPane {
		t.Fatalf("other session matched: %v", err)
	}
}
