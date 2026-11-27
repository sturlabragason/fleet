package mcp

import (
	"encoding/json"
	"path/filepath"
	"strings"
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
		{"fleet_wall", `{"action":"spin"}`, "action must be show, hide, preset, hide_all, restore, admiral_show, admiral_hide, present, present_on or present_off"},
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
// (fleet_instructions is answered in server.go before call, extension tools
// by extCall).
func TestEveryAdmiralToolHasHandler(t *testing.T) {
	for _, d := range toolDefs {
		if _, ext := extTools[d.Name]; ext || d.Name == instructionsToolDef.Name {
			continue
		}
		if toolHandlers[d.Name] == nil {
			t.Errorf("%s is listed but has no handler", d.Name)
		}
	}
}

// An unknown tool name is a JSON-RPC -32602 error, not an isError result,
// and it does not use up a pending reload note.
func TestUnknownToolIsProtocolError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	f, err := fleet.New(&tmux.Client{Socket: "fleet-test-unused", Config: "/dev/null"}, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	s := New(f, nil)
	s.Reloaded = true
	res, rerr := s.handle(request{Method: "tools/call", Params: json.RawMessage(`{"name":"invalid_tool_name","arguments":{}}`)})
	if res != nil || rerr == nil || rerr.Code != -32602 || rerr.Message != "Unknown tool: invalid_tool_name" {
		t.Fatalf("unknown tool: %v %+v", res, rerr)
	}
	if out, isErr := callText(t, s, "fleet_access", nil); isErr || !strings.Contains(out, reloadedNote) {
		t.Errorf("real tool after unknown: isError=%v, reload note kept=%v", isErr, strings.Contains(out, reloadedNote))
	}
}
