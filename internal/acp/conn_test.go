package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// peer is the agent side of an io.Pipe pair.
type peer struct {
	t  *testing.T
	in *bufio.Reader  // what the client wrote
	w  *io.PipeWriter // what the client reads
	r  *io.PipeReader
}

// recHandler records callbacks; perm answers permission requests.
type recHandler struct {
	mu      sync.Mutex
	events  []string
	perm    func(ctx context.Context, p RequestPermissionParams) (RequestPermissionResult, error)
	updates chan SessionNotification
}

func (h *recHandler) SessionUpdate(n SessionNotification) {
	h.mu.Lock()
	h.events = append(h.events, "u:"+n.Update.Text())
	h.mu.Unlock()
	if h.updates != nil {
		h.updates <- n
	}
}

func (h *recHandler) CopilotEvent(e CopilotEvent) {
	h.mu.Lock()
	h.events = append(h.events, "c:"+e.Type)
	h.mu.Unlock()
}

func (h *recHandler) RequestPermission(ctx context.Context, p RequestPermissionParams) (RequestPermissionResult, error) {
	if h.perm == nil {
		return Selected(p.Options[0].OptionID), nil
	}
	return h.perm(ctx, p)
}

func (h *recHandler) got() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.events...)
}

func newPair(t *testing.T, h Handler) (*Client, *peer) {
	t.Helper()
	cr, aw := io.Pipe() // agent writes, client reads
	ar, cw := io.Pipe() // client writes, agent reads
	cl := NewClient(cr, cw, h)
	p := &peer{t: t, in: bufio.NewReader(ar), w: aw, r: ar}
	t.Cleanup(func() {
		cl.Close()
		aw.Close()
		ar.Close()
	})
	return cl, p
}

func (p *peer) recv() Message {
	p.t.Helper()
	line, err := p.in.ReadBytes('\n')
	if err != nil {
		p.t.Fatalf("peer read: %v", err)
	}
	var m Message
	if err := json.Unmarshal(line, &m); err != nil {
		p.t.Fatalf("peer decode %q: %v", line, err)
	}
	return m
}

func (p *peer) send(s string) {
	p.t.Helper()
	if _, err := io.WriteString(p.w, s); err != nil {
		p.t.Fatalf("peer write: %v", err)
	}
}

func (p *peer) reply(id json.RawMessage, result string) {
	p.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`+"\n", id, result))
}

func update(text string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":%q}}}}`+"\n", text)
}

func waitDone(t *testing.T, cl *Client) {
	t.Helper()
	select {
	case <-cl.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done not closed")
	}
}

func TestFraming(t *testing.T) {
	huge := strings.Repeat("x", 2<<20)
	tests := []struct {
		name   string
		chunks []string
		want   []string
	}{
		{"partial line", []string{`{"jsonrpc":"2.0","method":"session/upd`, `ate","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hi"}}}}` + "\n"}, []string{"u:hi"}},
		{"blank lines", []string{"\n\r\n  \n", update("a"), "\n", update("b")}, []string{"u:a", "u:b"}},
		{"huge line", []string{update(huge)}, []string{"u:" + huge}},
		{"malformed skipped", []string{"{not json\n", `{"jsonrpc":"2.0"}` + "\n", update("ok")}, []string{"u:ok"}},
		{"last line without newline", []string{strings.TrimSuffix(update("tail"), "\n")}, []string{"u:tail"}},
		{"copilot event in order", []string{update("1"), `{"jsonrpc":"2.0","method":"github.com/copilot/sessionEvent","params":{"sessionId":"s","type":"session.idle"}}` + "\n", update("2")}, []string{"u:1", "c:session.idle", "u:2"}},
		{"unknown notification ignored", []string{`{"jsonrpc":"2.0","method":"x/y","params":{}}` + "\n", update("z")}, []string{"u:z"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &recHandler{}
			cl, p := newPair(t, h)
			go func() {
				for _, c := range tc.chunks {
					io.WriteString(p.w, c)
				}
				p.w.Close()
			}()
			waitDone(t, cl)
			if err := cl.Err(); err != nil {
				t.Fatalf("Err = %v, want nil on clean EOF", err)
			}
			got := h.got()
			if len(got) != len(tc.want) {
				t.Fatalf("got %d events, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("event %d = %.40q, want %.40q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestOutOfOrderResponses(t *testing.T) {
	cl, p := newPair(t, &recHandler{})
	type out struct {
		id  string
		err error
	}
	res := make(chan out, 2)
	go func() {
		id, err := cl.NewSession(context.Background(), "/a", nil)
		res <- out{id, err}
	}()
	m1 := p.recv()
	go func() {
		id, err := cl.NewSession(context.Background(), "/b", nil)
		res <- out{id, err}
	}()
	m2 := p.recv()
	var p1, p2 NewSessionParams
	json.Unmarshal(m1.Params, &p1)
	json.Unmarshal(m2.Params, &p2)
	if string(m1.ID) == string(m2.ID) {
		t.Fatalf("ids not unique: %s", m1.ID)
	}
	if p1.MCPServers == nil || !strings.Contains(string(m1.Params), `"mcpServers":[]`) {
		t.Errorf("mcpServers not an empty array: %s", m1.Params)
	}
	p.reply(m2.ID, `{"sessionId":"`+p2.Cwd+`"}`)
	p.reply(m1.ID, `{"sessionId":"`+p1.Cwd+`"}`)
	got := map[string]bool{}
	for range 2 {
		o := <-res
		if o.err != nil {
			t.Fatal(o.err)
		}
		got[o.id] = true
	}
	if !got["/a"] || !got["/b"] {
		t.Errorf("responses mismatched: %v", got)
	}
}

func TestStringIDResponseDoesNotMatchNumber(t *testing.T) {
	cl, p := newPair(t, &recHandler{})
	errc := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() {
		_, err := cl.NewSession(ctx, "/a", nil)
		errc <- err
	}()
	m := p.recv()
	p.send(`{"jsonrpc":"2.0","id":"` + string(m.ID) + `","result":{"sessionId":"x"}}` + "\n")
	if err := <-errc; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline (string id must not match number id)", err)
	}
}

func TestAgentRequests(t *testing.T) {
	tests := []struct {
		name, req, wantID string
		wantCode          int
		wantResult        string
		perm              func(context.Context, RequestPermissionParams) (RequestPermissionResult, error)
	}{
		{name: "permission number id", req: `{"jsonrpc":"2.0","id":7,"method":"session/request_permission","params":{"sessionId":"s","toolCall":{"toolCallId":"t1"},"options":[{"optionId":"ok","name":"Allow","kind":"allow_once"}]}}`,
			wantID: `7`, wantResult: `{"outcome":{"outcome":"selected","optionId":"ok"}}`},
		{name: "permission string id", req: `{"jsonrpc":"2.0","id":"abc-1","method":"session/request_permission","params":{"sessionId":"s","toolCall":{"toolCallId":"t1"},"options":[{"optionId":"ok","name":"Allow","kind":"allow_once"}]}}`,
			wantID: `"abc-1"`, wantResult: `{"outcome":{"outcome":"selected","optionId":"ok"}}`},
		{name: "fs read refused", req: `{"jsonrpc":"2.0","id":"r","method":"fs/read_text_file","params":{"path":"/etc/passwd"}}`, wantID: `"r"`, wantCode: CodeMethodNotFound},
		{name: "fs write refused", req: `{"jsonrpc":"2.0","id":2,"method":"fs/write_text_file","params":{}}`, wantID: `2`, wantCode: CodeMethodNotFound},
		{name: "unknown method", req: `{"jsonrpc":"2.0","id":3,"method":"terminal/create","params":{}}`, wantID: `3`, wantCode: CodeMethodNotFound},
		{name: "handler error", req: `{"jsonrpc":"2.0","id":4,"method":"session/request_permission","params":{"sessionId":"s","toolCall":{"toolCallId":"t"},"options":[]}}`, wantID: `4`, wantCode: CodeInternal,
			perm: func(context.Context, RequestPermissionParams) (RequestPermissionResult, error) {
				return RequestPermissionResult{}, errors.New("boom")
			}},
		{name: "bad params", req: `{"jsonrpc":"2.0","id":5,"method":"session/request_permission","params":[1]}`, wantID: `5`, wantCode: CodeInvalidParams},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, p := newPair(t, &recHandler{perm: tc.perm})
			p.send(tc.req + "\n")
			m := p.recv()
			if string(m.ID) != tc.wantID {
				t.Errorf("id = %s, want %s", m.ID, tc.wantID)
			}
			if tc.wantCode != 0 {
				if m.Error == nil || m.Error.Code != tc.wantCode {
					t.Fatalf("error = %+v, want code %d", m.Error, tc.wantCode)
				}
				return
			}
			if m.Error != nil || string(m.Result) != tc.wantResult {
				t.Errorf("result = %s err %+v, want %s", m.Result, m.Error, tc.wantResult)
			}
		})
	}
}

func TestPendingFailsOnEOFAndClose(t *testing.T) {
	t.Run("eof", func(t *testing.T) {
		cl, p := newPair(t, &recHandler{})
		errc := make(chan error, 1)
		go func() { _, err := cl.Prompt(context.Background(), "s", "hi"); errc <- err }()
		p.recv()
		p.w.Close()
		if err := <-errc; !errors.Is(err, ErrClosed) {
			t.Fatalf("err = %v, want ErrClosed", err)
		}
		waitDone(t, cl)
		if _, err := cl.NewSession(context.Background(), "/", nil); !errors.Is(err, ErrClosed) {
			t.Errorf("call after Done: %v", err)
		}
	})
	t.Run("read error", func(t *testing.T) {
		cl, p := newPair(t, &recHandler{})
		p.w.CloseWithError(errors.New("pipe broke"))
		waitDone(t, cl)
		if err := cl.Err(); err == nil || !strings.Contains(err.Error(), "pipe broke") {
			t.Errorf("Err = %v", err)
		}
	})
	t.Run("close", func(t *testing.T) {
		cl, p := newPair(t, &recHandler{})
		errc := make(chan error, 1)
		go func() { _, err := cl.Prompt(context.Background(), "s", "hi"); errc <- err }()
		p.recv()
		cl.Close()
		if err := <-errc; !errors.Is(err, ErrClosed) {
			t.Fatalf("err = %v, want ErrClosed", err)
		}
		if _, err := p.in.ReadBytes('\n'); err != io.EOF {
			t.Errorf("agent stdin not closed: %v", err)
		}
		if err := cl.Cancel("s"); !errors.Is(err, ErrClosed) {
			t.Errorf("Cancel after Close: %v", err)
		}
	})
}

func TestPermissionCtxCancelledOnClose(t *testing.T) {
	started := make(chan struct{})
	h := &recHandler{perm: func(ctx context.Context, p RequestPermissionParams) (RequestPermissionResult, error) {
		close(started)
		<-ctx.Done()
		return CancelledOutcome(), nil
	}}
	cl, p := newPair(t, h)
	go io.Copy(io.Discard, p.in)
	p.send(`{"jsonrpc":"2.0","id":1,"method":"session/request_permission","params":{"sessionId":"s","toolCall":{"toolCallId":"t"},"options":[]}}` + "\n")
	<-started
	cl.Close()
	// the handler returning is the assertion; the test would hang otherwise
}

// A Prompt in flight while the agent asks permission and streams updates:
// the permission handler blocks until answered, and nothing deadlocks.
func TestConcurrentPromptAndPermission(t *testing.T) {
	answer := make(chan string)
	asked := make(chan RequestPermissionParams, 1)
	h := &recHandler{perm: func(ctx context.Context, p RequestPermissionParams) (RequestPermissionResult, error) {
		asked <- p
		select {
		case id := <-answer:
			return Selected(id), nil
		case <-ctx.Done():
			return CancelledOutcome(), nil
		}
	}}
	cl, p := newPair(t, h)
	type out struct {
		r   StopReason
		err error
	}
	res := make(chan out, 1)
	go func() { r, err := cl.Prompt(context.Background(), "s1", "go"); res <- out{r, err} }()
	pm := p.recv()
	var pp PromptParams
	json.Unmarshal(pm.Params, &pp)
	if pp.SessionID != "s1" || len(pp.Prompt) != 1 || pp.Prompt[0].Text != "go" {
		t.Fatalf("prompt params %s", pm.Params)
	}
	p.send(update("before"))
	p.send(`{"jsonrpc":"2.0","id":"perm-1","method":"session/request_permission","params":{"sessionId":"s1","toolCall":{"toolCallId":"t1","title":"Run ls"},"options":[{"optionId":"a","name":"Allow","kind":"allow_once"},{"optionId":"r","name":"Reject","kind":"reject_once"}]}}` + "\n")
	q := <-asked
	// While the question is open, updates and other calls still flow.
	p.send(update("during"))
	cancelDone := make(chan error, 1)
	go func() { cancelDone <- cl.Cancel("s1") }()
	if m := p.recv(); m.Method != MethodSessionCancel {
		t.Fatalf("want session/cancel, got %+v", m)
	}
	if err := <-cancelDone; err != nil {
		t.Fatal(err)
	}
	answer <- q.Options[0].OptionID
	r := p.recv()
	if string(r.ID) != `"perm-1"` || !strings.Contains(string(r.Result), `"optionId":"a"`) {
		t.Fatalf("permission reply %+v %s", r, r.Result)
	}
	p.send(update("after"))
	p.reply(pm.ID, `{"stopReason":"cancelled"}`)
	o := <-res
	if o.err != nil || o.r != StopCancelled {
		t.Fatalf("prompt = %v %v", o.r, o.err)
	}
	// Updates sent before the response were dispatched before Prompt returned.
	got := h.got()
	want := []string{"u:before", "u:during", "u:after"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("events %v, want %v", got, want)
	}
}

func TestCallContextCancel(t *testing.T) {
	cl, p := newPair(t, &recHandler{})
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, err := cl.Prompt(ctx, "s", "x"); errc <- err }()
	m := p.recv()
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	p.reply(m.ID, `{"stopReason":"end_turn"}`) // late reply is skipped, not a panic
	sid := make(chan string, 1)
	go func() { id, _ := cl.NewSession(context.Background(), "/", nil); sid <- id }()
	m2 := p.recv()
	p.reply(m2.ID, `{"sessionId":"s9"}`)
	if got := <-sid; got != "s9" {
		t.Errorf("next call after a late reply = %q", got)
	}
}

func TestRPCErrorReturned(t *testing.T) {
	cl, p := newPair(t, &recHandler{})
	errc := make(chan error, 1)
	go func() { _, err := cl.Prompt(context.Background(), "s", "x"); errc <- err }()
	m := p.recv()
	p.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32000,"message":"nope"}}`+"\n", m.ID))
	var re *RPCError
	if err := <-errc; !errors.As(err, &re) || re.Code != -32000 || re.Message != "nope" {
		t.Fatalf("err = %v", err)
	}
}
