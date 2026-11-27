package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
)

// gateServer is an admiral server over mailHome (agents a and b, no git dir,
// so no close warning) whose CLI-backed tools are stubbed, with the client
// set as initialize would.
func gateServer(t *testing.T, client, version string) *Server {
	t.Helper()
	s := New(mailHome(t), nil)
	s.RunCLI = func(context.Context, string, ...string) (string, int, error) { return "ok", 0, nil }
	if client != "" {
		s.takeClient(json.RawMessage(fmt.Sprintf(`{"clientInfo":{"name":%q,"version":%q},"capabilities":{"elicitation":{}}}`, client, version)))
	}
	return s
}

// gatedCalls are the tools that need the user's yes, with arguments that get
// them to the gate. fleet_send's gate needs an agent on a prompt
// (TestSendConfirm in mcp_test.go); TestNativeGate covers its predicate.
var gatedCalls = []struct {
	tool   string
	args   map[string]any
	native bool // the Claude Code prompt stands in for confirmed=true
}{
	{"fleet_answer", map[string]any{"name": "a", "answer": "y"}, true},
	{"fleet_close", map[string]any{"name": "a"}, true},
	{"fleet_access_decide", map[string]any{"a": "a", "b": "b", "action": "grant"}, true},
	{"fleet_team", map[string]any{"action": "close", "team": "t"}, false},
	{"fleet_worktrees", map[string]any{"dir": "/tmp", "prune": true}, false},
	{"fleet_branches", map[string]any{"dir": "/tmp", "prune": true}, false},
	{"fleet_admiral_add", map[string]any{"name": "x", "client": "claude-desktop"}, false},
}

func TestConfirmGate(t *testing.T) {
	for _, client := range []struct{ name, version string }{
		{"", ""},                   // no initialize seen
		{"copilot-cli", "1.0.116"}, // another client
		{"claude-code", "2.1.200"}, // Claude Code before the gate worked
		{"claude-code", "garbage"},
		{"claude-code", "2.1.327"},
	} {
		native := client.name == "claude-code" && client.version == "2.1.327"
		for _, c := range gatedCalls {
			for _, confirmed := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s@%s/%s/%v", client.name, client.version, c.tool, confirmed), func(t *testing.T) {
					s := gateServer(t, client.name, client.version)
					a := map[string]any{"confirmed": confirmed}
					maps.Copy(a, c.args)
					out, _ := callText(t, s, c.tool, a)
					refused := strings.Contains(out, "not confirmed")
					want := !confirmed && (!native || !c.native)
					if refused != want {
						t.Fatalf("refused=%v, want %v: %q", refused, want, out)
					}
				})
			}
		}
	}
}

// A close with a warning (uncommitted work, a squad) still needs
// confirmed=true under Claude Code: the user must see it before the yes.
func TestNativeGate(t *testing.T) {
	s := gateServer(t, "claude-code", "2.1.327")
	for _, c := range []struct {
		g    gate
		want bool
	}{
		{gate{tool: "fleet_close"}, true},
		{gate{tool: "fleet_close", warn: ". Note: a has uncommitted changes"}, false},
		{gate{tool: "fleet_send", agent: "a"}, false},
		{gate{tool: "fleet_team"}, false},
	} {
		if got := s.confirmed(c.g, args{}); got != c.want {
			t.Errorf("%+v: confirmed %v, want %v", c.g, got, c.want)
		}
	}
	if err := s.confirm(gate{tool: "fleet_close", warn: ". Note: x"}, args{}); err == nil || !strings.HasSuffix(err.Error(), "confirmed=true. Note: x") {
		t.Errorf("warning not in refusal: %v", err)
	}
}

// listedTools runs tools/list the way the client sees it.
func listedTools(t *testing.T, s *Server) map[string]map[string]any {
	t.Helper()
	res, rerr := s.handle(request{Method: "tools/list"})
	if rerr != nil {
		t.Fatal(rerr.Message)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Tools []map[string]any }
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, tl := range got.Tools {
		out[tl["name"].(string)] = tl
	}
	return out
}

func TestToolsListMeta(t *testing.T) {
	for _, c := range []struct {
		client, version string
		meta            bool
	}{
		{"", "", false},
		{"copilot-cli", "1.0.116", false},
		{"claude-code", "2.1.245", false},
		{"claude-code", "2.1.246", true},
		{"claude-code", "2.2.0-beta", true},
	} {
		tools := listedTools(t, gateServer(t, c.client, c.version))
		if len(tools) != len(toolDefs) {
			t.Fatalf("%s@%s: %d tools, want %d", c.client, c.version, len(tools), len(toolDefs))
		}
		for name, tl := range tools {
			meta, has := tl["_meta"].(map[string]any)
			wantMeta := c.meta && nativeGated[name]
			if has != wantMeta {
				t.Errorf("%s@%s %s: _meta %v, want %v", c.client, c.version, name, has, wantMeta)
				continue
			}
			if !wantMeta {
				continue
			}
			if meta["anthropic/requiresUserInteraction"] != true {
				t.Errorf("%s: _meta %v", name, meta)
			}
			desc := tl["description"].(string)
			if !strings.Contains(desc, nativeNote) || strings.Contains(desc, askInChat) {
				t.Errorf("%s: description not rewritten: %q", name, desc)
			}
			if slices.Contains(tl["inputSchema"].(map[string]any)["required"].([]any), any("confirmed")) {
				t.Errorf("%s still requires confirmed", name)
			}
		}
	}
	// The served copy left the shared definitions alone.
	for _, d := range toolDefs {
		if nativeGated[d.Name] && (!strings.Contains(d.Description, askInChat) || !slices.Contains(d.InputSchema["required"].([]string), "confirmed")) {
			t.Errorf("%s: shared definition changed", d.Name)
		}
	}
}

// The client identity survives the re-exec: the new process never sees
// initialize, yet still serves Claude Code's tools/list (DESIGN §6.9).
func TestClientSurvivesReload(t *testing.T) {
	s, exe := reloadServer(t)
	s.Agent = ""
	execs := make(chan []string, 1)
	s.Probe = func(string) error { return nil }
	s.Exec = func(_ string, _, env []string) error { execs <- env; return nil }
	c, done := startServe(t, s)
	c.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"claude-code","version":"2.1.327"},"capabilities":{"elicitation":{}}}}` + "\n")
	c.next()
	replaceExe(t, exe, "v2")
	c.send(ping(2))
	env := <-execs
	<-done
	i := slices.IndexFunc(env, func(e string) bool { return strings.HasPrefix(e, envClient+"=") })
	if i < 0 {
		t.Fatalf("exec env lacks %s", envClient)
	}
	if slices.ContainsFunc(reloadEnv(env), func(e string) bool { return strings.HasPrefix(e, envClient+"=") }) {
		t.Fatalf("reloadEnv kept %s", envClient)
	}

	t.Setenv(envClient, strings.TrimPrefix(env[i], envClient+"="))
	s2, _ := reloadServer(t)
	s2.Agent = ""
	s2.Reloaded = true
	c2, _ := startServe(t, s2)
	c2.next() // tools/list_changed
	c2.send(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}` + "\n")
	c2.next()
	if !s2.nativePrompt() || string(s2.client.Capabilities) != `{"elicitation":{}}` {
		t.Fatalf("client after reload: %+v", s2.client)
	}
	if _, ok := listedTools(t, s2)["fleet_close"]["_meta"]; !ok {
		t.Fatal("no _meta after reload")
	}
	if os.Getenv(envClient) != "" {
		t.Fatal("client identity left in the environment")
	}
}
