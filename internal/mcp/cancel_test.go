package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func (r *rpc) send(m map[string]any) {
	r.t.Helper()
	b, _ := json.Marshal(m)
	if _, err := r.in.Write(append(b, '\n')); err != nil {
		r.t.Fatal(err)
	}
}

// replies reads responses until one for id last, keyed by id; progress
// notifications are skipped.
func (r *rpc) replies(last string) (ids []string, text map[string]string) {
	r.t.Helper()
	text = map[string]string{}
	for r.out.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Result struct {
				Content []struct{ Text string } `json:"content"`
			} `json:"result"`
		}
		json.Unmarshal(r.out.Bytes(), &m)
		if len(m.ID) == 0 {
			continue
		}
		id := string(m.ID)
		ids = append(ids, id)
		if len(m.Result.Content) > 0 {
			text[id] = m.Result.Content[0].Text
		}
		if id == last {
			return ids, text
		}
	}
	r.t.Fatalf("no reply %s, got %v", last, ids)
	return nil, nil
}

func cancelMsg(id int) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": id}}
}

func pingMsg(id int) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "method": "ping"}
}

// A client that gives up on fleet_wait (Copilot cancels it when the user
// types into an ACP agent's pane) must not leave the server blocked (live,
// 2026-11-12). A cancel for the wait's id ends it with no reply; another
// call ends it too (ADR-72), and then it is answered.
func TestWaitEndsOnCancelAndNextCall(t *testing.T) {
	for _, tc := range []struct {
		name  string
		then  map[string]any // sent while the wait (id 7) blocks
		want  []string       // reply ids, in order
		wait7 string         // the wait's reply starts with this
	}{
		{"cancel for its id: no reply", cancelMsg(7), []string{"8"}, ""},
		{"cancel for another id: answered", cancelMsg(3), []string{"7", "8"}, "interrupted after"},
		{"next call: answered", map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/call",
			"params": map[string]any{"name": "fleet_inbox"}}, []string{"7", "9", "8"}, "interrupted after"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fastWait(t)
			r := startRPC(t, NewAgent(mailHome(t), "a"))
			start := time.Now()
			r.send(map[string]any{"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": map[string]any{
				"name": "fleet_wait", "arguments": map[string]any{"timeout_s": 20}}})
			// The pipe is unbuffered and the loop is inside the wait, so
			// these writes return once the wait's reader took them.
			go func() {
				r.send(tc.then)
				r.send(pingMsg(8))
			}()
			ids, text := r.replies("8")
			if strings.Join(ids, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("replies %v, want %v", ids, tc.want)
			}
			if tc.wait7 != "" && !strings.HasPrefix(text["7"], tc.wait7) {
				t.Fatalf("wait reply %q, want %q…", text["7"], tc.wait7)
			}
			if d := time.Since(start); d > time.Second {
				t.Fatalf("the ended wait held the server for %s", d)
			}
		})
	}
}

// A slow CLI-backed tool stops on a cancel for its id and gets no reply; a
// following request does not stop it (clients send read-only calls in
// parallel) and is answered after it.
func TestCLIToolCancel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cancel bool
		want   []string
	}{
		{"cancel for its id", true, []string{"2", "3"}},
		{"a following request", false, []string{"1", "2", "3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := cliServer(t, "", 0)
			started, release, ended := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			s.RunCLI = func(ctx context.Context, _ string, _ ...string) (string, int, error) {
				close(started)
				select {
				case <-ctx.Done():
				case <-release:
				case <-time.After(10 * time.Second):
				}
				ended <- ctx.Err()
				return "listed", 0, nil
			}
			r := startRPC(t, s)
			r.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
				"name": "fleet_worktrees", "arguments": map[string]any{"dir": "/r"}}})
			<-started
			r.send(pingMsg(2))
			if tc.cancel {
				r.send(cancelMsg(1))
			}
			go func() {
				r.send(pingMsg(3)) // returns once the reader took ping 2 and moved on
				close(release)
			}()
			ids, _ := r.replies("3")
			if strings.Join(ids, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("replies %v, want %v", ids, tc.want)
			}
			if err := <-ended; (err != nil) != tc.cancel {
				t.Fatalf("command ctx %v, cancelled want %v", err, tc.cancel)
			}
		})
	}
}

func TestCancels(t *testing.T) {
	for _, tc := range []struct {
		line, id string
		want     bool
	}{
		{`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":7}}`, "7", true},
		{`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"a"}}`, `"a"`, true},
		{`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":7}}`, "8", false},
		{`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":7}}`, "", false},
		{`{"jsonrpc":"2.0","id":7,"method":"ping"}`, "7", false},
		{`not json`, "7", false},
	} {
		id, ok := cancelID([]byte(tc.line))
		if got := ok && idKey(id) == idKey(json.RawMessage(tc.id)); got != tc.want {
			t.Errorf("cancel of %s in %s = %v", tc.id, tc.line, got)
		}
	}
}

// Lines queued behind a slow call count for the call that runs after it: a
// queued cancel for that call's id drops it, and a queued request ends a
// fleet_wait that starts after it.
func TestQueuedLinesReachTheNextCall(t *testing.T) {
	worktrees := func(id int) map[string]any {
		return map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{
			"name": "fleet_worktrees", "arguments": map[string]any{"dir": "/r"}}}
	}
	wait := map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "fleet_wait", "arguments": map[string]any{"timeout_s": 20}}}
	for _, tc := range []struct {
		name   string
		queued []map[string]any
		want   []string
	}{
		{"cancel for the queued call", []map[string]any{worktrees(2), cancelMsg(2)}, []string{"1", "3"}},
		{"request after a queued wait", []map[string]any{wait, pingMsg(4)}, []string{"1", "2", "4", "3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := cliServer(t, "", 0)
			started, release := make(chan struct{}), make(chan struct{})
			var calls int
			s.RunCLI = func(ctx context.Context, _ string, _ ...string) (string, int, error) {
				if calls++; calls > 1 {
					select { // the cancel was queued before this call began
					case <-ctx.Done():
					case <-time.After(10 * time.Second):
					}
					return "late", 0, nil
				}
				close(started)
				<-release
				return "listed", 0, nil
			}
			r := startRPC(t, s)
			r.send(worktrees(1))
			<-started
			for _, m := range tc.queued {
				r.send(m)
			}
			go func() {
				r.send(pingMsg(3)) // returns once the queued lines were checked
				close(release)
			}()
			start := time.Now()
			ids, _ := r.replies("3")
			if strings.Join(ids, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("replies %v, want %v", ids, tc.want)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Fatalf("a queued line was missed: took %s", d)
			}
		})
	}
}
