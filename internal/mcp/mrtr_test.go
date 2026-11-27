package mcp

import (
	"fmt"
	"maps"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// modernClose is a 2026-07-28 fleet_close of agent a; extra holds the
// retry's MRTR fields.
func modernClose(id int, caps map[string]any, args, extra map[string]any) map[string]any {
	a := map[string]any{"name": "a"}
	maps.Copy(a, args)
	return modernCall(id, caps, "fleet_close", a, extra)
}

// modernCall is a 2026-07-28 tools/call; extra holds the retry's MRTR fields.
func modernCall(id int, caps map[string]any, tool string, a, extra map[string]any) map[string]any {
	p := map[string]any{"name": tool, "arguments": a, "_meta": map[string]any{
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
			return nil, map[string]any{"requestState": st + "A", "inputResponses": map[string]any{confirmKey: answer("accept", true)}}
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

// A multi-target send asks once, naming every target on a prompt, before it
// delivers to anyone: the retry then delivers to each target once.
func TestMRTRMultiSend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte("[yn]\ncommand = \"sh\"\nargs = [\"-c\", \"printf 'Approve? [y/n] '; cat\"]\npatterns = \"fake\"\n"), 0o644)
	f, err := fleet.New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"p1", "p2"} {
		if _, err := f.Spawn(n, "yn", home, ""); err != nil {
			t.Fatal(err)
		}
	}
	for end := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		w1, _ := f.Prompting("p1")
		w2, _ := f.Prompting("p2")
		if w1 != "" && w2 != "" {
			break
		}
		if time.Now().After(end) {
			t.Fatal("fake agents never showed their prompt")
		}
	}
	s := New(f, nil)
	s.Fill = func() (int, int, bool) { return 0, 0, false }
	r := startRPC(t, s)
	args := map[string]any{"names": []string{"p1", "p2"}, "text": "y", "confirmed": true}
	r.send(modernCall(1, formCaps, "fleet_send", args, nil))
	res, _ := r.msg()["result"].(map[string]any)
	req, _ := res["inputRequests"].(map[string]any)[confirmKey].(map[string]any)
	msg, _ := req["params"].(map[string]any)["message"].(string)
	if res["resultType"] != "input_required" || !strings.Contains(msg, "p1 (") || !strings.Contains(msg, "p2 (") {
		t.Fatalf("want one dialog naming both prompts, got %v", res)
	}
	if w, _ := f.Prompting("p1"); w == "" {
		t.Fatal("p1 answered before the user said yes")
	}
	yes := map[string]any{"requestState": res["requestState"], "inputResponses": map[string]any{confirmKey: answer("accept", true)}}
	r.send(modernCall(2, formCaps, "fleet_send", args, yes))
	if text, isErr := toolReply(t, r.msg()); isErr || text != "p1: sent\np2: sent" {
		t.Fatalf("retry: %q isError=%v", text, isErr)
	}
}
