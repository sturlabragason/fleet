package mcp

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
)

// msg reads the next message the server wrote.
func (r *rpc) msg() map[string]any {
	r.t.Helper()
	if !r.out.Scan() {
		r.t.Fatal("no message")
	}
	var m map[string]any
	if err := json.Unmarshal(r.out.Bytes(), &m); err != nil {
		r.t.Fatal(err)
	}
	return m
}

// toolReply is a tools/call reply's text and isError.
func toolReply(t *testing.T, m map[string]any) (string, bool) {
	t.Helper()
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("not a tool result: %v", m)
	}
	return res["content"].([]any)[0].(map[string]any)["text"].(string), res["isError"].(bool)
}

func closeCall(id int, a map[string]any) map[string]any {
	args := map[string]any{"name": "a"}
	maps.Copy(args, a)
	return map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": "fleet_close", "arguments": args}}
}

// dialog reads the elicitation/create fleet sends and checks its shape.
func dialog(r *rpc) any {
	r.t.Helper()
	m := r.msg()
	p, _ := m["params"].(map[string]any)
	if m["method"] != "elicitation/create" || m["id"] == nil || p == nil {
		r.t.Fatalf("want elicitation/create, got %v", m)
	}
	if msg, _ := p["message"].(string); !strings.HasPrefix(msg, "Close agent a?") {
		r.t.Fatalf("dialog message %q", msg)
	}
	sch, _ := p["requestedSchema"].(map[string]any)
	if b, _ := json.Marshal(sch["required"]); string(b) != `["confirm"]` {
		r.t.Fatalf("schema %v", sch)
	}
	return m["id"]
}

func onBoard(s *Server, name string) bool {
	_, ok := s.F.Store.Load().Agents[name]
	return ok
}

func elicitServer(t *testing.T) *Server {
	t.Helper()
	return capsServer(t, "copilot-cli", "1.0.116", `{"elicitation":{}}`)
}

// The user's answer in the dialog is the gate; confirmed=true does not
// pre-approve, and a late answer after a timeout gets no reply.
func TestElicitAnswers(t *testing.T) {
	accept := func(v bool) map[string]any {
		return map[string]any{"result": map[string]any{"action": "accept", "content": map[string]any{"confirm": v}}}
	}
	for _, tc := range []struct {
		name      string
		confirmed bool
		answer    map[string]any // nil: nobody answers
		want      string
	}{
		{"accept yes", false, accept(true), "closed a"},
		{"accept no", false, accept(false), "the user said no"},
		{"decline", false, map[string]any{"result": map[string]any{"action": "decline"}}, "the user said no"},
		{"decline beats confirmed", true, map[string]any{"result": map[string]any{"action": "decline"}}, "the user said no"},
		{"cancel", false, map[string]any{"result": map[string]any{"action": "cancel"}}, "closed fleet's dialog"},
		{"client error", false, map[string]any{"error": map[string]any{"code": -32603, "message": "no terminal"}}, "could not show fleet's dialog: no terminal"},
		{"timeout", false, nil, "nobody answered fleet's dialog"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := elicitServer(t)
			s.dialogWait = 50 * time.Millisecond
			if tc.answer != nil {
				s.dialogWait = time.Minute
			}
			r := startRPC(t, s)
			r.send(closeCall(1, map[string]any{"confirmed": tc.confirmed}))
			id := dialog(r)
			answer := map[string]any{"jsonrpc": "2.0", "id": id}
			maps.Copy(answer, tc.answer)
			if tc.answer != nil {
				r.send(answer)
			} else if m := r.msg(); m["method"] != "notifications/cancelled" {
				t.Fatalf("timeout did not withdraw the dialog: %v", m)
			}
			text, isErr := toolReply(t, r.msg())
			ok := tc.want == "closed a"
			if !strings.Contains(text, tc.want) || isErr == ok || onBoard(s, "a") == ok {
				t.Fatalf("got %q isError=%v on board=%v", text, isErr, onBoard(s, "a"))
			}
			if tc.answer == nil { // the late answer is not answered
				answer["result"] = accept(true)["result"]
				r.send(answer)
				r.send(pingMsg(3))
				if m := r.msg(); m["id"] != float64(3) {
					t.Fatalf("want the ping reply, got %v", m)
				}
			}
		})
	}
}

// While the dialog is open, a cancel of the tools/call ends it with no
// reply (and withdraws the dialog); another request waits and is answered
// after the call.
func TestElicitDuringDialog(t *testing.T) {
	t.Run("cancelled call", func(t *testing.T) {
		s := elicitServer(t)
		r := startRPC(t, s)
		r.send(closeCall(1, nil))
		id := dialog(r)
		r.send(cancelMsg(1))
		if m := r.msg(); m["method"] != "notifications/cancelled" || m["params"].(map[string]any)["requestId"] != id {
			t.Fatalf("dialog not withdrawn: %v", m)
		}
		r.send(pingMsg(2))
		if m := r.msg(); m["id"] != float64(2) {
			t.Fatalf("want only the ping reply, got %v", m)
		}
		if !onBoard(s, "a") {
			t.Fatal("closed after a cancel")
		}
	})
	t.Run("request held", func(t *testing.T) {
		s := elicitServer(t)
		r := startRPC(t, s)
		r.send(closeCall(1, nil))
		id := dialog(r)
		r.send(pingMsg(2))
		r.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"action": "accept", "content": map[string]any{"confirm": true}}})
		if text, isErr := toolReply(t, r.msg()); isErr || !strings.Contains(text, "closed a") {
			t.Fatalf("close: %q", text)
		}
		if m := r.msg(); m["id"] != float64(2) {
			t.Fatalf("want the held ping answered next, got %v", m)
		}
	})
}

// A client that did not declare elicitation never gets a dialog (the spec:
// MUST NOT); confirmed=true stays the gate.
func TestElicitFallback(t *testing.T) {
	s := capsServer(t, "copilot-cli", "1.0.116", `{"elicitation":{"url":{}}}`)
	r := startRPC(t, s)
	r.send(closeCall(1, nil))
	if text, isErr := toolReply(t, r.msg()); !isErr || !strings.Contains(text, "confirmed=true") {
		t.Fatalf("unconfirmed close: %q", text)
	}
	r.send(closeCall(2, map[string]any{"confirmed": true}))
	if text, isErr := toolReply(t, r.msg()); isErr || !strings.Contains(text, "closed a") {
		t.Fatalf("confirmed close: %q", text)
	}
}

func TestElicits(t *testing.T) {
	for caps, want := range map[string]bool{
		``:                                       false,
		`{}`:                                     false,
		`{"elicitation":null}`:                   false,
		`{"elicitation":{}}`:                     true, // 2025-06-18
		`{"elicitation":{"form":{}}}`:            true,
		`{"elicitation":{"form":{},"url":{}}}`:   true,
		`{"elicitation":{"url":{}}}`:             false,
		`{"elicitation":{"form":null,"url":{}}}`: false,
		`{"elicitation":{"other":{}}}`:           false,
	} {
		s := &Server{client: clientInfo{Capabilities: json.RawMessage(caps)}}
		if got := s.elicits(); got != want {
			t.Errorf("%s: elicits %v, want %v", caps, got, want)
		}
	}
}

// A client that elicits is never told to ask in chat or to pass confirmed,
// and Claude Code's permission-prompt mark is dropped: one prompt per action.
func TestDialogToolsList(t *testing.T) {
	for name, tl := range listedTools(t, capsServer(t, "claude-code", "2.1.327", `{"elicitation":{"form":{}}}`)) {
		if _, has := tl["_meta"]; has {
			t.Errorf("%s: _meta for a client that elicits", name)
		}
		desc := tl["description"].(string)
		if strings.Contains(strings.ToLower(desc), "ask the user first") || strings.Contains(desc, "confirmed=true only") {
			t.Errorf("%s still asks in chat: %q", name, desc)
		}
		sch := tl["inputSchema"].(map[string]any)
		if req, _ := sch["required"].([]any); len(req) > 0 {
			for _, r := range req {
				if r == "confirmed" {
					t.Errorf("%s requires confirmed", name)
				}
			}
		}
		if p, ok := sch["properties"].(map[string]any)["confirmed"]; ok && !strings.Contains(p.(map[string]any)["description"].(string), "dialog") {
			t.Errorf("%s: confirmed described as %v", name, p)
		}
	}
}

// A tools/call cancelled while it was queued behind a slow call opens no
// dialog and gets no reply.
func TestElicitQueuedCancel(t *testing.T) {
	s := elicitServer(t)
	started, release := make(chan struct{}), make(chan struct{})
	s.RunCLI = func(context.Context, string, ...string) (string, int, error) {
		close(started)
		<-release
		return "listed", 0, nil
	}
	r := startRPC(t, s)
	r.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
		"name": "fleet_worktrees", "arguments": map[string]any{"dir": "/r"}}})
	<-started
	r.send(closeCall(2, nil))
	r.send(cancelMsg(2))
	go func() {
		r.send(pingMsg(3)) // returns once the queued lines were checked
		close(release)
	}()
	if ids, _ := r.replies("3"); strings.Join(ids, " ") != "1 3" {
		t.Fatalf("messages with ids %v, want 1 3 (no dialog)", ids)
	}
	if !onBoard(s, "a") {
		t.Fatal("closed after a cancel")
	}
}

// Every response is the router's: a stray one never reaches the loop,
// where it would end a fleet_wait.
func TestDeliverTakesResponses(t *testing.T) {
	s := &Server{}
	for line, want := range map[string]bool{
		`{"jsonrpc":"2.0","id":"x","result":{}}`:                    true,
		`{"jsonrpc":"2.0","id":7,"error":{"code":1,"message":"m"}}`: true,
		`{"jsonrpc":"2.0","id":7,"method":"ping"}`:                  false,
		`{"jsonrpc":"2.0","method":"notifications/cancelled"}`:      false,
		`not json`: false,
	} {
		if got := s.deliver([]byte(line)); got != want {
			t.Errorf("deliver(%s) = %v, want %v", line, got, want)
		}
	}
}

// Team send asks in a dialog only when the team exists and a member is on a
// prompt; a refusal never tells a dialog client to ask in chat.
func TestElicitTeamSend(t *testing.T) {
	s := elicitServer(t)
	r := startRPC(t, s)
	send := func(id int, team string) map[string]any {
		return map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{
			"name": "fleet_team", "arguments": map[string]any{"action": "send", "team": team, "text": "hi", "confirmed": true}}}
	}
	r.send(send(1, "nope"))
	if _, isErr := toolReply(t, r.msg()); !isErr {
		t.Fatal("send to a missing team not refused")
	}
	r.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "fleet_team", "arguments": map[string]any{"action": "add", "team": "t", "agents": []string{"a"}}}})
	toolReply(t, r.msg())
	r.send(send(3, "t"))
	if text, _ := toolReply(t, r.msg()); !strings.Contains(text, "a") {
		t.Fatalf("team send: %q", text)
	}
	if got := s.dialogHint(fleet.WaitingError("Allow?").Error()); strings.Contains(got, fleet.AskThenConfirm) || !strings.Contains(got, "dialog") {
		t.Fatalf("hint for a dialog client: %q", got)
	}
}
