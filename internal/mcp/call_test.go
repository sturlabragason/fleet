package mcp

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// The argument checks Server.call makes before it touches tmux or state,
// pinned so the handler table keeps every message.
func TestCallArgumentErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	f, err := fleet.New(&tmux.Client{Socket: "fleet-test-unused", Config: "/dev/null"}, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	s := New(f, nil)
	for _, c := range []struct{ tool, args, want string }{
		{"fleet_nope", `{}`, `unknown tool "fleet_nope"`},
		{"fleet_peek", `{"name":`, "bad arguments: unexpected end of JSON input"},
		{"fleet_spawn", `{"dir":"/tmp"}`, "name is required"},
		{"fleet_adopt", `{}`, "pass exactly one of session_id or query"},
		{"fleet_adopt", `{"session_id":"a","query":"b"}`, "pass exactly one of session_id or query"},
		{"fleet_park", `{}`, "name is required"},
		{"fleet_wall", `{"action":"spin"}`, "action must be show, hide, preset, hide_all, restore, admiral_show or admiral_hide"},
		{"fleet_note", `{"text":"  "}`, "text is required"},
		{"fleet_access_decide", `{"action":"maybe","a":"x","b":"y","confirmed":true}`, "action must be grant, deny or revoke"},
		{"fleet_access_decide", `{"action":"grant","a":"x","b":"y"}`, "not confirmed: ask the user, then call again with confirmed=true"},
		{"fleet_handoff", `{"summary":" "}`, "summary is required"},
	} {
		if _, err := s.call(c.tool, json.RawMessage(c.args)); err == nil || err.Error() != c.want {
			t.Errorf("%s %s: %v, want %q", c.tool, c.args, err, c.want)
		}
	}
	out, err := s.call("fleet_access", json.RawMessage(`{}`))
	if err != nil || out == "" {
		t.Errorf("fleet_access: %q %v", out, err)
	}
	if out, err := s.call("fleet_note", json.RawMessage(`{"text":"remember this"}`)); err != nil || out != "noted" {
		t.Errorf("fleet_note: %q %v", out, err)
	}
	if out, err := s.call("fleet_note", json.RawMessage(`{"text":"keep this","pin":true}`)); err != nil || out != "pinned" {
		t.Errorf("fleet_note pin: %q %v", out, err)
	}
}

// Every tool the admiral server lists has a handler in the table
// (fleet_instructions is answered in server.go before call).
func TestEveryAdmiralToolHasHandler(t *testing.T) {
	for _, d := range toolDefs {
		if toolHandlers[d.Name] == nil && d.Name != instructionsToolDef.Name {
			t.Errorf("%s is listed but has no handler", d.Name)
		}
	}
}
