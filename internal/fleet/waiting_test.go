package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/tmux"
)

// The live bug this fixes: a pane sitting on Claude's import dialog showed
// "working" on its border while the board said "waiting". One refresh must
// put the same answer in the board, in the pane tag the border reads, and in
// waiting_for — and waiting_for is the question, not the key hint under the
// options.
func TestMenuPaneAgreesWithBoard(t *testing.T) {
	home := t.TempDir()
	noPlaybook(t, home)
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	fixture, err := filepath.Abs("../status/testdata/claude/waiting-imports.txt")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[dialog]
command = "sh"
args = ["-c", "cat `+fixture+`; sleep 60"]
patterns = "claude"
`), 0o644)
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Spawn("d1", "dialog", home, ""); err != nil {
		t.Fatal(err)
	}
	var a = struct {
		status, line, tag string
	}{}
	for i := 0; i < 40; i++ {
		as, err := f.Refresh()
		if err != nil {
			t.Fatal(err)
		}
		if len(as) == 1 && as[0].Status == "waiting" {
			a.status, a.line = as[0].Status, as[0].LastLine
			a.tag = paneOption(t, tc, as[0].PaneID, "@fleet_status")
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if a.status != "waiting" {
		t.Fatal("an unnumbered ❯ dialog never read as waiting")
	}
	if a.tag != "waiting" {
		t.Errorf("border says %q while the board says %q", a.tag, a.status)
	}
	if a.line != "Allow external CLAUDE.md file imports?" {
		t.Errorf("waiting_for %q", a.line)
	}
	// And the dialog can be answered by label, without numbers on screen.
	c, err := f.ChooseLabel("d1", "yes, allow", "external CLAUDE.md")
	if err != nil {
		t.Fatalf("choose by label: %v", err)
	}
	if c.Label != "2. Yes, allow external imports" {
		t.Errorf("picked %q", c.Label)
	}
	if _, err := f.ChooseLabel("d1", "nope", ""); err == nil {
		t.Error("a label that matches nothing was accepted")
	}
}
