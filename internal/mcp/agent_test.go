package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/wall"
)

// rpc drives one Server over pipes.
type rpc struct {
	t   *testing.T
	in  *io.PipeWriter
	out *bufio.Scanner
	id  int
}

func startRPC(t *testing.T, s *Server) *rpc {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go s.Serve(inR, outW)
	t.Cleanup(func() { inW.Close() })
	return &rpc{t: t, in: inW, out: bufio.NewScanner(outR)}
}

func (r *rpc) call(method string, params any) map[string]any {
	r.t.Helper()
	r.id++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": r.id, "method": method, "params": params})
	r.in.Write(append(b, '\n'))
	if !r.out.Scan() {
		r.t.Fatal("no response")
	}
	var m map[string]any
	json.Unmarshal(r.out.Bytes(), &m)
	return m
}

func (r *rpc) tool(name string, a map[string]any) (string, bool) {
	r.t.Helper()
	res := r.call("tools/call", map[string]any{"name": name, "arguments": a})["result"].(map[string]any)
	return res["content"].([]any)[0].(map[string]any)["text"].(string), res["isError"].(bool)
}

func mailHome(t *testing.T) *fleet.Fleet {
	t.Helper()
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := fleet.New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	// Board entries only: no status, so no nudge and no tmux needed.
	f.Store.Update(func(b *board.Board) error {
		for _, n := range []string{"a", "b"} {
			b.Agents[n] = &board.Agent{Name: n, Profile: "fake", Dir: home}
		}
		return nil
	})
	return f
}

func TestAgentMode(t *testing.T) {
	f := mailHome(t)
	ra, rb := startRPC(t, NewAgent(f, "a")), startRPC(t, NewAgent(f, "b"))
	init := ra.call("initialize", map[string]any{"protocolVersion": ProtocolVersion})["result"].(map[string]any)
	if init["instructions"] != AgentInstructions {
		t.Error("agent mode must send the agent instructions")
	}
	var names []string
	for _, tl := range ra.call("tools/list", nil)["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	sort.Strings(names)
	if got := strings.Join(names, " "); got != "fleet_access_request fleet_access_respond fleet_access_revoke fleet_agents fleet_inbox fleet_msg_send fleet_report fleet_wait" {
		t.Fatalf("agent tools: %s", got)
	}
	if msg, isErr := ra.tool("fleet_status", nil); !isErr || !strings.Contains(msg, "unknown tool") {
		t.Fatalf("admiral tool in agent mode: %v %q", isErr, msg)
	}
	if msg, isErr := ra.tool("fleet_msg_send", map[string]any{"to": "b", "text": "hi"}); !isErr || !strings.HasPrefix(msg, "no access to b: request it with fleet_access_request") {
		t.Fatalf("send without access: %q", msg)
	}
	if msg, isErr := ra.tool("fleet_access_request", map[string]any{"to": "b", "reason": "share API"}); isErr || !strings.Contains(msg, "pending") {
		t.Fatalf("request: %q", msg)
	}
	if msg, _ := rb.tool("fleet_agents", nil); !strings.Contains(msg, "a | fake |") || !strings.Contains(msg, "pending (asks you)") || !strings.HasSuffix(msg, "\n\n(1 unread)") {
		t.Fatalf("agents: %q", msg)
	}
	if msg, isErr := rb.tool("fleet_access_respond", map[string]any{"from": "a", "decision": "grant"}); isErr {
		t.Fatalf("respond: %q", msg)
	}
	ra.tool("fleet_inbox", nil) // the grant notice
	if msg, isErr := ra.tool("fleet_msg_send", map[string]any{"to": "b", "text": "schema is v2"}); isErr || !strings.HasPrefix(msg, "sent to b") {
		t.Fatalf("send: %q", msg)
	}
	if msg, isErr := ra.tool("fleet_msg_send", map[string]any{"to": "b", "text": "merged, nothing to do", "needs_action": false}); isErr || !strings.HasPrefix(msg, "sent to b (FYI: ") {
		t.Fatalf("FYI send: %q", msg)
	}
	msg, isErr := rb.tool("fleet_inbox", nil)
	if isErr || !strings.HasPrefix(msg, InboxHeader) || !strings.Contains(msg, "a→b msg] schema is v2") || !strings.Contains(msg, "a→b msg fyi] merged, nothing to do") || strings.Contains(msg, "unread)") {
		t.Fatalf("inbox: %q", msg)
	}
	if msg, _ := rb.tool("fleet_inbox", nil); !strings.HasPrefix(msg, "no new messages") {
		t.Fatalf("second inbox: %q", msg)
	}
	if msg, _ := rb.tool("fleet_inbox", map[string]any{"all": true, "limit": 2}); !strings.Contains(msg, "schema is v2") {
		t.Fatalf("inbox all: %q", msg)
	}
	ghost := startRPC(t, NewAgent(f, "ghost"))
	if msg, isErr := ghost.tool("fleet_agents", nil); !isErr || msg != `no agent "ghost"` {
		t.Fatalf("unknown agent: %q", msg)
	}
}

func TestSupervisorAccessTools(t *testing.T) {
	f := mailHome(t)
	r := startRPC(t, New(f, wall.New(f.T, fleet.Session)))
	r.call("initialize", map[string]any{"protocolVersion": ProtocolVersion})
	if msg, isErr := r.tool("fleet_access_decide", map[string]any{"a": "a", "b": "b", "action": "grant"}); !isErr || !strings.Contains(msg, "not confirmed") {
		t.Fatalf("unconfirmed decide: %q", msg)
	}
	if len(f.Mail().All()) != 0 {
		t.Fatal("unconfirmed decide changed state")
	}
	if msg, isErr := r.tool("fleet_access_decide", map[string]any{"a": "a", "b": "b", "action": "grant", "confirmed": true}); isErr {
		t.Fatalf("decide: %q", msg)
	}
	if msg, _ := r.tool("fleet_access", nil); !strings.Contains(msg, "a | b | granted |  | user |") || !strings.HasPrefix(msg, "agent_mail = team:") {
		t.Fatalf("access: %q", msg)
	}
	if _, err := f.MailSend("a", "b", "line one\nline two"); err != nil {
		t.Fatal(err)
	}
	if msg, _ := r.tool("fleet_access", map[string]any{"limit": 5}); !strings.Contains(msg, "a -> b | line one line two") {
		t.Fatalf("access log: %q", msg)
	}
}
