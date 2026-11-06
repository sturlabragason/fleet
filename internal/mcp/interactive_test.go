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

// fleet#1: fleet_send refuses a pane the user is typing in unless forced,
// and fleet_status marks the agent.
func TestSendRefusesWhileUserTypes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte("[cat]\ncommand = \"sh\"\nargs = [\"-c\", \"cat\"]\npatterns = \"fake\"\n"), 0o644)
	f, err := fleet.New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.Spawn("u1", "cat", home, "")
	if err != nil {
		t.Fatal(err)
	}
	f.Clients = func() []fleet.ClientSeen { return []fleet.ClientSeen{{Pane: a.PaneID, Active: time.Now()}} }
	s := New(f, wall.New(tc, fleet.Session))
	s.Fill = func() (int, int, bool) { return 0, 0, false }
	msg, isErr := callText(t, s, "fleet_send", map[string]any{"name": "u1", "text": "hello"})
	if !isErr || !strings.Contains(msg, "the user typed in u1's pane") || !strings.Contains(msg, "force=true") {
		t.Fatalf("send while typing: %q (isErr=%v)", msg, isErr)
	}
	if st, _ := callText(t, s, "fleet_status", nil); !strings.Contains(st, "(interactive)") {
		t.Errorf("status does not mark the agent: %q", st)
	}
	if msg, isErr := callText(t, s, "fleet_send", map[string]any{"name": "u1", "text": "hello", "force": true}); isErr {
		t.Fatalf("forced send: %q", msg)
	}
}
