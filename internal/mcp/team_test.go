package mcp

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/wall"
)

func TestFleetTeam(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte("[yn]\ncommand = \"sh\"\nargs = [\"-c\", \"printf 'Approve? [y/n] '; cat\"]\npatterns = \"fake\"\n"), 0o644)
	f, err := fleet.New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a1", "a2"} {
		if _, err := f.Spawn(n, "yn", home, ""); err != nil {
			t.Fatal(err)
		}
	}
	s := New(f, wall.New(tc, fleet.Session))
	s.Fill = func() (int, int, bool) { return 0, 0, false }

	if out, isErr := callText(t, s, "fleet_team", map[string]any{"action": "add", "team": "alpha", "agents": []string{"a1", "ghost"}}); isErr || !strings.Contains(out, "added a1, ghost to alpha") {
		t.Fatalf("add: %q", out)
	}
	if out, _ := callText(t, s, "fleet_team", map[string]any{"action": "add", "team": "beta", "agents": []string{"ghost"}}); !strings.Contains(out, "moved ghost from alpha") {
		t.Errorf("move not reported: %q", out)
	}
	callText(t, s, "fleet_team", map[string]any{"action": "add", "team": "alpha", "agents": []string{"ghost"}})
	out, isErr := callText(t, s, "fleet_team", map[string]any{"action": "list"})
	if isErr || !strings.Contains(out, "alpha | blue | a1, ghost (not running)") || !strings.Contains(out, "beta | pink | \n") {
		t.Errorf("list: %q", out)
	}
	out, isErr = callText(t, s, "fleet_team", map[string]any{"action": "send", "team": "alpha", "text": "team hello", "confirmed": true}) // a1 sits on a [y/n] prompt
	if isErr || out != "alpha: sent: a1; skipped: ghost (not running)" {
		t.Errorf("send: %q", out)
	}
	var pane string
	for i := 0; i < 30 && !strings.Contains(pane, "team hello"); i++ {
		time.Sleep(100 * time.Millisecond)
		pane, _ = f.Peek("a1", 0)
	}
	if !strings.Contains(pane, "team hello") {
		t.Errorf("a1 never got the text: %q", pane)
	}
	if out, isErr := callText(t, s, "fleet_team", map[string]any{"action": "close", "team": "alpha"}); !isErr || !strings.Contains(out, "not confirmed") {
		t.Errorf("unconfirmed close must be refused: %q", out)
	}
	if len(f.Store.Load().Agents) != 2 {
		t.Error("refused close still closed agents")
	}
	if _, isErr := callText(t, s, "fleet_team", map[string]any{"action": "color", "team": "alpha", "color": "green"}); !isErr {
		t.Error("status colour accepted as a team colour")
	}
	if _, isErr := callText(t, s, "fleet_team", map[string]any{"action": "nope", "team": "alpha"}); !isErr {
		t.Error("unknown action accepted")
	}
	st, _ := callText(t, s, "fleet_status", nil)
	if !strings.HasPrefix(st, "admiral: ?\nname | team | squad | profile | driver | status") || !strings.Contains(st, "a1 | alpha |  | yn | tmux | ") || !strings.Contains(st, "a2 |  |  | yn | tmux | ") {
		t.Errorf("status: %q", st)
	}
	if out, isErr := callText(t, s, "fleet_team", map[string]any{"action": "close", "team": "alpha", "confirmed": true}); isErr || !strings.Contains(out, "closed: a1") {
		t.Errorf("close: %q", out)
	}
}
