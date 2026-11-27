package mcp

import (
	"maps"
	"strings"
	"testing"
)

// modernClose is a 2026-07-28 fleet_close of agent a; extra holds the
// retry's MRTR fields.
func modernClose(id int, caps map[string]any, args, extra map[string]any) map[string]any {
	a := map[string]any{"name": "a"}
	maps.Copy(a, args)
	p := map[string]any{"name": "fleet_close", "arguments": a, "_meta": map[string]any{
		metaVersion:                                  ModernVersion,
		"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "copilot-cli", "version": "1.0.116"},
		"io.modelcontextprotocol/clientCapabilities": caps,
	}}
	maps.Copy(p, extra)
	return map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": p}
}

var formCaps = map[string]any{"elicitation": map[string]any{"form": map[string]any{}}}

// inputRequired checks fleet's input_required result and returns its state.
func inputRequired(t *testing.T, m map[string]any) string {
	t.Helper()
	res, _ := m["result"].(map[string]any)
	if res["resultType"] != "input_required" || res["content"] != nil {
		t.Fatalf("want input_required, got %v", m)
	}
	req, _ := res["inputRequests"].(map[string]any)[confirmKey].(map[string]any)
	p, _ := req["params"].(map[string]any)
	if req["method"] != "elicitation/create" || p["mode"] != "form" || !strings.HasPrefix(p["message"].(string), "Close agent a?") || p["requestedSchema"] == nil {
		t.Fatalf("input request %v", req)
	}
	st, _ := res["requestState"].(string)
	if st == "" {
		t.Fatal("no requestState")
	}
	return st
}

func answer(action string, yes bool) map[string]any {
	return map[string]any{"action": action, "content": map[string]any{"confirm": yes}}
}

// A modern client with elicitation gets input_required, and only its retry
// with the user's yes and fleet's state for this exact call closes.
func TestMRTRConfirm(t *testing.T) {
	for _, tc := range []struct {
		name  string
		retry func(state string) (args, extra map[string]any)
		want  string
	}{
		{"accept", func(st string) (map[string]any, map[string]any) {
			return nil, map[string]any{"requestState": st, "inputResponses": map[string]any{confirmKey: answer("accept", true)}}
		}, "closed a"},
		{"decline", func(st string) (map[string]any, map[string]any) {
			return nil, map[string]any{"requestState": st, "inputResponses": map[string]any{confirmKey: answer("decline", false)}}
		}, "the user said no"},
		{"tampered state", func(st string) (map[string]any, map[string]any) {
			return nil, map[string]any{"requestState": st[:len(st)-2] + "AA", "inputResponses": map[string]any{confirmKey: answer("accept", true)}}
		}, "did not come with fleet's state"},
		{"state of another call", func(st string) (map[string]any, map[string]any) {
			return map[string]any{"name": "b"}, map[string]any{"requestState": st, "inputResponses": map[string]any{confirmKey: answer("accept", true)}}
		}, "for a different call"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := capsServer(t, "", "", "")
			r := startRPC(t, s)
			// confirmed=true does not pre-approve: the dialog still asks.
			r.send(modernClose(1, formCaps, map[string]any{"confirmed": true}, nil))
			st := inputRequired(t, r.msg())
			if !onBoard(s, "a") {
				t.Fatal("closed before the user answered")
			}
			args, extra := tc.retry(st)
			if args == nil {
				args = map[string]any{"confirmed": true}
			}
			r.send(modernClose(2, formCaps, args, extra))
			text, isErr := toolReply(t, r.msg())
			ok := tc.want == "closed a"
			if !strings.Contains(text, tc.want) || isErr == ok || onBoard(s, "a") == ok {
				t.Fatalf("got %q isError=%v on board=%v", text, isErr, onBoard(s, "a"))
			}
		})
	}
}

// A replayed yes is spent, and a retry without an answer asks again.
func TestMRTRReplay(t *testing.T) {
	s := capsServer(t, "", "", "")
	r := startRPC(t, s)
	r.send(modernClose(1, formCaps, nil, nil))
	st := inputRequired(t, r.msg())
	r.send(modernClose(2, formCaps, nil, map[string]any{"requestState": st}))
	inputRequired(t, r.msg())
	yes := map[string]any{"requestState": st, "inputResponses": map[string]any{confirmKey: answer("accept", true)}}
	r.send(modernClose(3, formCaps, nil, yes))
	if text, isErr := toolReply(t, r.msg()); isErr {
		t.Fatalf("accept: %q", text)
	}
	r.send(modernClose(4, formCaps, nil, yes))
	if text, isErr := toolReply(t, r.msg()); !isErr || !strings.Contains(text, "already used") {
		t.Fatalf("replay: %q", text)
	}
}

// A modern client without form elicitation never gets input_required (the
// spec: MUST NOT); confirmed=true stays the gate.
func TestMRTRFallback(t *testing.T) {
	s := capsServer(t, "", "", "")
	r := startRPC(t, s)
	caps := map[string]any{"elicitation": map[string]any{"url": map[string]any{}}}
	r.send(modernClose(1, caps, nil, nil))
	if text, isErr := toolReply(t, r.msg()); !isErr || !strings.Contains(text, "confirmed=true") {
		t.Fatalf("unconfirmed close: %q", text)
	}
	r.send(modernClose(2, caps, map[string]any{"confirmed": true}, nil))
	if text, isErr := toolReply(t, r.msg()); isErr || !strings.Contains(text, "closed a") {
		t.Fatalf("confirmed close: %q", text)
	}
}
