package mcp

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// ADR-203: fleet_spawn plan=... refuses a plan it can't run before writing it, and
// with no text lists the plans.
func TestPlanToolValidatesAndLists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	f, err := fleet.New(&tmux.Client{Socket: "fleet-test-unused", Config: "/dev/null"}, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	s := New(f, nil)
	text := `[task.a]
profile = "copilot"
dir = "/tmp"
prompt = "p"
review = { profile = "copilot" }`
	if _, err := s.call("fleet_spawn", json.RawMessage(`{"name":"p1","plan":`+strconv.Quote(text)+`}`)); err == nil || !strings.Contains(err.Error(), "same vendor") {
		t.Fatalf("same-vendor review: %v", err)
	}
	if out, err := s.call("fleet_status", json.RawMessage(`{"plan":"*"}`)); err != nil || !strings.Contains(out, "no plans") {
		t.Fatalf("list: %q %v", out, err)
	}
}
