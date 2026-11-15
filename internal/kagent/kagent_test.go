package kagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/acp"
	"github.com/sturlabragason/fleet/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }

// call is one JSON-RPC request the fake gateway received.
type call struct {
	Method string
	Msg    Message
	TaskID string // CancelTask's id
	Header http.Header
}

// gateway is a fake kagent A2A gateway; reply answers each send.
type gateway struct {
	t     *testing.T
	mu    sync.Mutex
	calls []call
	reply func(w http.ResponseWriter, r *http.Request, c call)
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/agents/kagent/claude" {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Method string
		Params struct {
			Message Message
			ID      string
		}
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		g.t.Errorf("bad request: %v", err)
	}
	c := call{Method: req.Method, Msg: req.Params.Message, TaskID: req.Params.ID, Header: r.Header.Clone()}
	g.mu.Lock()
	g.calls = append(g.calls, c)
	g.mu.Unlock()
	if req.Method == "CancelTask" {
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"1","result":{"id":"t1"}}`)
		return
	}
	g.reply(w, r, c)
}

func (g *gateway) sends() []call {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []call
	for _, c := range g.calls {
		if c.Method == "SendStreamingMessage" {
			out = append(out, c)
		}
	}
	return out
}

// sse writes each result as one server-sent event.
func sse(w http.ResponseWriter, results ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, r := range results {
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":\"1\",\"result\":%s}\n\n", r)
	}
}

func status(state, text string) string {
	return fmt.Sprintf(`{"statusUpdate":{"taskId":"t1","contextId":"c1","status":{"state":%q,"message":{"messageId":"m","role":"ROLE_AGENT","parts":[{"text":%q}]}}}}`, state, text)
}

// recorder is the ACP client's handler: it keeps the agent's text.
type recorder struct {
	mu   sync.Mutex
	text strings.Builder
}

func (r *recorder) SessionUpdate(n acp.SessionNotification) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n.Update.Kind == acp.UpdateAgentMessage {
		r.text.WriteString(n.Update.Text())
	}
}

func (r *recorder) RequestPermission(context.Context, acp.RequestPermissionParams) (acp.RequestPermissionResult, error) {
	return acp.CancelledOutcome(), nil
}
func (r *recorder) CopilotEvent(acp.CopilotEvent) {}

func (r *recorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.text.String()
}

// start serves a bridge on pipes and connects an ACP client to it, as
// fleet's acp-host does.
func start(t *testing.T, url, stateDir string) (*acp.Client, *recorder) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	b := &Bridge{Gateway: Gateway{URL: url, Agent: "kagent/claude", User: "tester"}, StateDir: stateDir, Cluster: "dev",
		Repo: func(context.Context, string) (string, string) { return "https://example.org/r.git", "fleet/a1" }}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = b.Serve(t.Context(), inR, outW)
		outW.Close()
	}()
	rec := &recorder{}
	cl := acp.NewClient(outR, inW, rec)
	t.Cleanup(func() {
		inW.Close()
		<-done
		cl.Close()
	})
	if _, err := cl.Initialize(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	return cl, rec
}

func TestPromptStreamsAndContinues(t *testing.T) {
	g := &gateway{t: t, reply: func(w http.ResponseWriter, _ *http.Request, _ call) {
		sse(w,
			`{"task":{"id":"t1","contextId":"c1","status":{"state":"TASK_STATE_SUBMITTED"}}}`,
			status("TASK_STATE_WORKING", "looking"),
			`{"artifactUpdate":{"taskId":"t1","contextId":"c1","artifact":{"artifactId":"a","parts":[{"text":"all done"}]}}}`,
			`{"statusUpdate":{"taskId":"t1","contextId":"c1","status":{"state":"TASK_STATE_COMPLETED"}}}`)
	}}
	srv := httptest.NewServer(g)
	defer srv.Close()
	dir := t.TempDir()
	cl, rec := start(t, srv.URL, dir)
	id, err := cl.NewSession(t.Context(), "/work", []acp.MCPServer{{Name: "fleet-peer", Command: "fleet"}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := cl.Prompt(t.Context(), id, "fix the bug")
	if err != nil || res.StopReason != acp.StopEndTurn {
		t.Fatalf("prompt: %v %v", res, err)
	}
	if got := rec.String(); got != "looking\n\nall done" {
		t.Errorf("agent text = %q", got)
	}
	if _, err := cl.Prompt(t.Context(), id, "and the test"); err != nil {
		t.Fatal(err)
	}
	s := g.sends()
	if len(s) != 2 {
		t.Fatalf("sends = %d", len(s))
	}
	first, second := Text(s[0].Msg.Parts), Text(s[1].Msg.Parts)
	if s[0].Msg.ContextID != "" || !strings.Contains(first, "cluster dev") || !strings.Contains(first, "branch fleet/a1") || !strings.HasSuffix(first, "fix the bug") {
		t.Errorf("first send: ctx %q text %q", s[0].Msg.ContextID, first)
	}
	if s[1].Msg.ContextID != "c1" || second != "and the test" {
		t.Errorf("second send: ctx %q text %q", s[1].Msg.ContextID, second)
	}
	if h := s[0].Header; h.Get("A2A-Version") != "1.0" || h.Get("X-User-Id") != "tester" {
		t.Errorf("headers: %v", h)
	}

	// A woken agent resumes the same kagent conversation.
	cl2, _ := start(t, srv.URL, dir)
	if err := cl2.ResumeSession(t.Context(), id, "/work", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cl2.Prompt(t.Context(), id, "status?"); err != nil {
		t.Fatal(err)
	}
	if s := g.sends(); s[2].Msg.ContextID != "c1" {
		t.Errorf("resumed send ctx = %q", s[2].Msg.ContextID)
	}
	if err := cl2.ResumeSession(t.Context(), "kagent-unknown", "/work", nil); err == nil {
		t.Error("resuming an unknown session should fail")
	}
}

func TestTaskEndStates(t *testing.T) {
	tests := []struct {
		name     string
		reply    func(w http.ResponseWriter)
		stop     acp.StopReason
		wantErr  string
		text     string
		nextTask string // the task id the next send answers
	}{
		{name: "plain JSON task", reply: func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":"1","result":{"task":{"id":"t1","contextId":"c1","status":{"state":"TASK_STATE_COMPLETED","message":{"messageId":"m","role":"ROLE_AGENT","parts":[{"text":"done"}]}}}}}`)
		}, stop: acp.StopEndTurn, text: "done"},
		{name: "a2a 0.3 message", reply: func(w http.ResponseWriter) {
			sse(w, `{"kind":"message","messageId":"m","contextId":"c1","role":"agent","parts":[{"kind":"text","text":"hi"}]}`)
		}, stop: acp.StopEndTurn, text: "hi"},
		{name: "failed", reply: func(w http.ResponseWriter) {
			sse(w, status("TASK_STATE_FAILED", "model quota exceeded"))
		}, wantErr: "model quota exceeded"},
		{name: "rpc error", reply: func(w http.ResponseWriter) {
			sse(w)
			fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":\"1\",\"error\":{\"code\":-32001,\"message\":\"TaskNotFound\"}}\n\n")
		}, wantErr: "TaskNotFound"},
		{name: "input required", reply: func(w http.ResponseWriter) {
			sse(w, status("TASK_STATE_INPUT_REQUIRED", "which branch?"))
		}, stop: acp.StopEndTurn, text: "which branch?", nextTask: "t1"},
		{name: "http error", reply: func(w http.ResponseWriter) {
			http.Error(w, "forbidden", http.StatusForbidden)
		}, wantErr: "HTTP 403"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := &gateway{t: t, reply: func(w http.ResponseWriter, _ *http.Request, _ call) { tc.reply(w) }}
			srv := httptest.NewServer(g)
			defer srv.Close()
			cl, rec := start(t, srv.URL, t.TempDir())
			id, err := cl.NewSession(t.Context(), "/work", nil)
			if err != nil {
				t.Fatal(err)
			}
			res, err := cl.Prompt(t.Context(), id, "go")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || res.StopReason != tc.stop {
				t.Fatalf("prompt: %v %v", res, err)
			}
			if got := rec.String(); got != tc.text {
				t.Errorf("text = %q, want %q", got, tc.text)
			}
			if _, err := cl.Prompt(t.Context(), id, "again"); err != nil {
				t.Fatal(err)
			}
			if got := g.sends()[1].Msg.TaskID; got != tc.nextTask {
				t.Errorf("next send task = %q, want %q", got, tc.nextTask)
			}
		})
	}
}

// A cancelled turn that answered an input-required task cancels it in kagent
// and leaves the session free of it.
func TestCancel(t *testing.T) {
	started := make(chan struct{})
	g := &gateway{t: t}
	g.reply = func(w http.ResponseWriter, r *http.Request, _ call) {
		switch len(g.sends()) {
		case 1:
			sse(w, status("TASK_STATE_INPUT_REQUIRED", "which?"))
		case 2:
			sse(w, status("TASK_STATE_WORKING", "busy"))
			w.(http.Flusher).Flush()
			close(started)
			<-r.Context().Done()
		default:
			sse(w, status("TASK_STATE_COMPLETED", "ok"))
		}
	}
	srv := httptest.NewServer(g)
	defer srv.Close()
	cl, rec := start(t, srv.URL, t.TempDir())
	id, err := cl.NewSession(t.Context(), "/work", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Prompt(t.Context(), id, "ask me"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		res acp.PromptResult
		err error
	}
	got := make(chan result, 1)
	go func() {
		res, err := cl.Prompt(t.Context(), id, "main")
		got <- result{res, err}
	}()
	<-started
	waitFor(t, func() bool { return strings.HasSuffix(rec.String(), "busy") }) // the event with the task id
	if err := cl.Cancel(id); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-got:
		if r.err != nil || r.res.StopReason != acp.StopCancelled {
			t.Fatalf("prompt: %v %v", r.res, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("prompt not cancelled")
	}
	waitFor(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		for _, c := range g.calls {
			if c.Method == "CancelTask" && c.TaskID == "t1" {
				return true
			}
		}
		return false
	})
	if _, err := cl.Prompt(t.Context(), id, "next"); err != nil {
		t.Fatal(err)
	}
	if s := g.sends(); s[1].Msg.TaskID != "t1" || s[2].Msg.TaskID != "" {
		t.Errorf("task ids sent: %q then %q, want t1 then none", s[1].Msg.TaskID, s[2].Msg.TaskID)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestState(t *testing.T) {
	for in, want := range map[string]string{
		"TASK_STATE_COMPLETED": "completed", "TASK_STATE_INPUT_REQUIRED": "input-required",
		"TASK_STATE_CANCELLED": "canceled", "canceled": "canceled", "auth-required": "auth-required",
	} {
		if got := State(in); got != want {
			t.Errorf("State(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestForward(t *testing.T) {
	tests := []struct {
		name, script, want, wantErr string
	}{
		{name: "port", script: "echo \"$@\" > \"$0.args\"\necho 'Forwarding from 127.0.0.1:43210 -> 8083'\nexec sleep 60\n",
			want: "http://127.0.0.1:43210"},
		{name: "error", script: "echo 'error: context \"nope\" does not exist' >&2\nexit 1\n",
			wantErr: `context "nope" does not exist`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "kubectl")
			if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+tc.script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			url, err := Forward{Context: "dev", Namespace: "kagent", Service: "kagent-controller", Port: 8083, Timeout: 10 * time.Second}.Start(ctx)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || url != tc.want {
				t.Fatalf("Start = %q, %v", url, err)
			}
			args, _ := os.ReadFile(bin + ".args")
			if got := strings.TrimSpace(string(args)); got != "--context dev -n kagent port-forward --address 127.0.0.1 svc/kagent-controller :8083" {
				t.Errorf("kubectl args = %q", got)
			}
		})
	}
}
