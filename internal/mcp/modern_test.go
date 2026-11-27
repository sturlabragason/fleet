package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// modernLine is a 2026-07-28 request: version, client and capabilities in
// params._meta. params holds the other fields as JSON object members ("" for none).
func modernLine(id any, version, method, params string, caps string) string {
	idj, _ := json.Marshal(id)
	meta := fmt.Sprintf(`"_meta":{%q:%q,"io.modelcontextprotocol/clientInfo":{"name":"claude-code","version":"2.1.400"},"io.modelcontextprotocol/clientCapabilities":%s}`,
		metaVersion, version, caps)
	if params != "" {
		meta += "," + params
	}
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":%q,"params":{%s}}`+"\n", idj, method, meta)
}

func legacyLine(id int, method, params string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":{%s}}`+"\n", id, method, params)
}

const legacyInit = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"other","version":"1"},"capabilities":{}}}` + "\n"

// A scripted modern client and a scripted legacy client against the same
// server code: each case sends its lines and checks the last response.
func TestDualEra(t *testing.T) {
	call := `"name":"fleet_instructions","arguments":{}`
	for _, tc := range []struct {
		name    string
		admiral bool
		lines   []string
		check   func(t *testing.T, s *Server, r map[string]any)
	}{
		{"modern discover", false, []string{modernLine("d-1", ModernVersion, "server/discover", "", "{}")}, func(t *testing.T, s *Server, r map[string]any) {
			t.Helper()
			res := r["result"].(map[string]any)
			if res["resultType"] != "complete" || res["ttlMs"] != 0.0 || res["cacheScope"] != "private" {
				t.Fatalf("discover %v", res)
			}
			if got := fmt.Sprint(res["supportedVersions"]); got != "[2026-07-28 2025-11-25 2025-06-18]" {
				t.Fatalf("supportedVersions %s", got)
			}
			if res["instructions"] != AgentInstructions || s.served != instructionsHash(AgentInstructions) {
				t.Fatalf("instructions or served hash: %q", s.served)
			}
			info := res["_meta"].(map[string]any)[metaServer].(map[string]any)
			if info["name"] != "fleet" || info["version"] == "" || res["capabilities"].(map[string]any)["tools"] == nil {
				t.Fatalf("serverInfo/capabilities %v", res)
			}
		}},
		{"modern tools/list", false, []string{modernLine(2, ModernVersion, "tools/list", "", "{}")}, func(t *testing.T, _ *Server, r map[string]any) {
			t.Helper()
			res := r["result"].(map[string]any)
			if res["resultType"] != "complete" || res["ttlMs"] != 0.0 || res["cacheScope"] != "private" || len(res["tools"].([]any)) == 0 {
				t.Fatalf("tools/list %v", res)
			}
		}},
		{"modern tools/call", false, []string{modernLine(3, ModernVersion, "tools/call", call, "{}")}, func(t *testing.T, _ *Server, r map[string]any) {
			t.Helper()
			res := r["result"].(map[string]any)
			if res["resultType"] != "complete" || res["isError"] != false || res["_meta"] == nil {
				t.Fatalf("tools/call %v", res)
			}
		}},
		{"modern unsupported version", false, []string{modernLine(4, "1900-01-01", "tools/list", "", "{}")}, func(t *testing.T, _ *Server, r map[string]any) {
			t.Helper()
			e := r["error"].(map[string]any)
			data := e["data"].(map[string]any)
			if e["code"] != -32022.0 || data["requested"] != "1900-01-01" || !slices.Contains(data["supported"].([]any), any(ModernVersion)) {
				t.Fatalf("error %v", e)
			}
		}},
		{"modern ping removed", false, []string{modernLine(5, ModernVersion, "ping", "", "{}")}, func(t *testing.T, _ *Server, r map[string]any) {
			t.Helper()
			if r["error"].(map[string]any)["code"] != -32601.0 {
				t.Fatalf("ping %v", r)
			}
		}},
		{"modern listen unsupported version", false, []string{modernLine("l", "2099-01-01", "subscriptions/listen", `"notifications":{"toolsListChanged":true}`, "{}")}, func(t *testing.T, _ *Server, r map[string]any) {
			t.Helper()
			if r["error"].(map[string]any)["code"] != -32022.0 {
				t.Fatalf("listen %v", r)
			}
		}},
		{"modern initialize removed", false, []string{modernLine(6, ModernVersion, "initialize", `"protocolVersion":"2025-11-25"`, "{}")}, func(t *testing.T, s *Server, r map[string]any) {
			t.Helper()
			if r["error"].(map[string]any)["code"] != -32601.0 || s.client.Protocol != "" {
				t.Fatalf("modern initialize %v, client %+v", r, s.client)
			}
		}},
		{"modern non-string version", false, []string{`{"jsonrpc":"2.0","id":7,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":5}}}` + "\n"}, func(t *testing.T, _ *Server, r map[string]any) {
			t.Helper()
			if e := r["error"].(map[string]any); e["code"] != -32022.0 || e["data"].(map[string]any)["requested"] != "5" {
				t.Fatalf("non-string version %v", r)
			}
		}},
		{"modern bad clientInfo stays modern", false, []string{`{"jsonrpc":"2.0","id":8,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":"x","io.modelcontextprotocol/clientCapabilities":{}}}}` + "\n"}, func(t *testing.T, _ *Server, r map[string]any) {
			t.Helper()
			if r["result"].(map[string]any)["resultType"] != "complete" {
				t.Fatalf("bad clientInfo %v", r)
			}
		}},
		{"legacy initialize", false, []string{legacyInit}, func(t *testing.T, _ *Server, r map[string]any) {
			t.Helper()
			res := r["result"].(map[string]any)
			if res["protocolVersion"] != "2025-11-25" || res["resultType"] != nil || res["serverInfo"] == nil {
				t.Fatalf("initialize %v", res)
			}
		}},
		{"legacy tools/list", false, []string{legacyInit, legacyLine(2, "tools/list", "")}, func(t *testing.T, _ *Server, r map[string]any) {
			t.Helper()
			res := r["result"].(map[string]any)
			if _, ok := res["ttlMs"]; ok || res["resultType"] != nil || res["cacheScope"] != nil {
				t.Fatalf("legacy tools/list carries modern fields: %v", res)
			}
		}},
		{"legacy tools/call", false, []string{legacyInit, legacyLine(3, "tools/call", call)}, func(t *testing.T, _ *Server, r map[string]any) {
			t.Helper()
			res := r["result"].(map[string]any)
			if res["resultType"] != nil || res["_meta"] != nil || res["isError"] != false {
				t.Fatalf("legacy tools/call %v", res)
			}
		}},
		{"legacy ping", false, []string{legacyInit, ping(4)}, func(t *testing.T, _ *Server, r map[string]any) {
			t.Helper()
			if res, ok := r["result"].(map[string]any); !ok || len(res) != 0 {
				t.Fatalf("ping %v", r)
			}
		}},
		{"legacy listen", false, []string{legacyInit, legacyLine(5, "subscriptions/listen", `"notifications":{"toolsListChanged":true}`)}, func(t *testing.T, _ *Server, r map[string]any) {
			t.Helper()
			if r["error"].(map[string]any)["code"] != -32601.0 {
				t.Fatalf("legacy listen %v", r)
			}
		}},
		// Capabilities come from each modern request, not from initialize:
		// the eliciting legacy client gets the dialog tools, the modern
		// claude-code request without elicitation in the same process gets
		// the native mark.
		{"per-request capabilities", true, []string{
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"other","version":"1"},"capabilities":{"elicitation":{}}}}` + "\n",
			modernLine(2, ModernVersion, "tools/list", "", `{}`),
		}, func(t *testing.T, s *Server, r map[string]any) {
			t.Helper()
			for _, tl := range r["result"].(map[string]any)["tools"].([]any) {
				m := tl.(map[string]any)
				if m["name"] == "fleet_close" && m["_meta"] == nil {
					t.Fatalf("modern claude-code tools/list lacks requiresUserInteraction: %v", m)
				}
			}
			if !s.elicits() {
				t.Fatal("legacy client's elicitation lost")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := reloadServer(t)
			if tc.admiral {
				s.Agent = ""
			}
			c, _ := startServe(t, s)
			var r map[string]any
			for _, l := range tc.lines {
				c.send(l)
				r = c.next()
			}
			tc.check(t, s, r)
		})
	}
}

// subscriptions/listen is acknowledged, a cancelled one closes silently, and
// the open one crosses a hot reload: the new process sends list_changed on
// it, and no bare legacy notification to this modern-only client.
func TestListenAcrossReload(t *testing.T) {
	s, exe := reloadServer(t)
	execs := make(chan []string, 1)
	s.Probe = func(string) error { return nil }
	s.Exec = func(_ string, _, env []string) error { execs <- env; return nil }
	c, done := startServe(t, s)
	filter := `"notifications":{"toolsListChanged":true,"promptsListChanged":true}`
	c.send(modernLine("listen-1", ModernVersion, "subscriptions/listen", filter, "{}"))
	ack := c.next()
	p := ack["params"].(map[string]any)
	if ack["method"] != "notifications/subscriptions/acknowledged" || p["_meta"].(map[string]any)[metaSub] != "listen-1" {
		t.Fatalf("ack %v", ack)
	}
	if got := fmt.Sprint(p["notifications"]); got != "map[toolsListChanged:true]" {
		t.Fatalf("acknowledged %s, want tools only", got)
	}
	c.send(modernLine("listen-2", ModernVersion, "subscriptions/listen", filter, "{}"))
	c.next()
	c.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"listen-2"}}` + "\n")
	// The listen requests hold no place in the lane: the next call answers.
	c.send(modernLine(3, ModernVersion, "tools/list", "", "{}"))
	if r := c.next(); r["id"] != 3.0 {
		t.Fatalf("after listen: %v", r)
	}

	replaceExe(t, exe, "v2")
	c.send(modernLine(4, ModernVersion, "tools/list", "", "{}"))
	env := <-execs
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(env, func(e string) bool { return strings.HasPrefix(e, envListen+"=") })
	if i < 0 {
		t.Fatalf("exec env lacks %s", envListen)
	}
	if slices.ContainsFunc(reloadEnv(env), func(e string) bool { return strings.HasPrefix(e, envListen+"=") }) {
		t.Fatalf("reloadEnv kept %s", envListen)
	}

	t.Setenv(envListen, strings.TrimPrefix(env[i], envListen+"="))
	s2, _ := reloadServer(t)
	s2.Reloaded = true
	c2, _ := startServe(t, s2)
	n := c2.next()
	if n["method"] != "notifications/tools/list_changed" || n["params"].(map[string]any)["_meta"].(map[string]any)[metaSub] != "listen-1" {
		t.Fatalf("first message after reload %v, want list_changed on listen-1 only", n)
	}
	c2.send(modernLine(5, ModernVersion, "tools/list", "", "{}"))
	if r := c2.next(); r["id"] != 5.0 {
		t.Fatalf("after reload: %v (a second list_changed went out?)", r)
	}
	if os.Getenv(envListen) != "" {
		t.Fatal("listen state left in the environment")
	}
}
