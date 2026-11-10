package fake

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// acpHarness drives ServeACP in-process over two pipes.
type acpHarness struct {
	t     *testing.T
	w     *io.PipeWriter
	lines chan string
	errc  chan error
	exit  atomic.Int32
}

func startACP(t *testing.T, opt ACPOptions) *acpHarness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	h := &acpHarness{t: t, w: inW, lines: make(chan string, 64), errc: make(chan error, 1)}
	h.exit.Store(-1)
	if opt.StateDir == "" {
		opt.StateDir = t.TempDir()
	}
	n := 0
	if opt.NewID == nil {
		opt.NewID = func() string { n++; return fmt.Sprintf("s%d", n) }
	}
	opt.Exit = func(code int) { h.exit.Store(int32(code)) }
	go func() {
		h.errc <- ServeACP(inR, outW, opt)
		outW.Close()
	}()
	go func() {
		sc := bufio.NewScanner(outR)
		for sc.Scan() {
			h.lines <- sc.Text()
		}
		close(h.lines)
	}()
	t.Cleanup(func() { inW.Close(); outR.Close() })
	return h
}

func (h *acpHarness) send(frame string) {
	h.t.Helper()
	if _, err := io.WriteString(h.w, frame+"\n"); err != nil {
		h.t.Fatal(err)
	}
}

// canon re-encodes a JSON frame so key order and spacing don't matter.
func canon(t *testing.T, s string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("bad json %q: %v", s, err)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// expect reads the next frames and compares each exactly.
func (h *acpHarness) expect(want ...string) {
	h.t.Helper()
	for i, w := range want {
		select {
		case got, ok := <-h.lines:
			if !ok {
				h.t.Fatalf("frame %d: output ended, want %s", i, w)
			}
			if canon(h.t, got) != canon(h.t, w) {
				h.t.Fatalf("frame %d:\n got %s\nwant %s", i, got, w)
			}
		case <-time.After(5 * time.Second):
			h.t.Fatalf("frame %d: timeout, want %s", i, w)
		}
	}
}

// quiet asserts nothing more is written for a short while.
func (h *acpHarness) quiet() {
	h.t.Helper()
	select {
	case got := <-h.lines:
		h.t.Fatalf("unexpected frame %s", got)
	case <-time.After(100 * time.Millisecond):
	}
}

// done closes the input and waits for ServeACP to return.
func (h *acpHarness) done() error {
	h.t.Helper()
	h.w.Close()
	select {
	case err := <-h.errc:
		return err
	case <-time.After(5 * time.Second):
		h.t.Fatal("ServeACP did not return")
		return nil
	}
}

const optIn = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"fs":{"readTextFile":false,"writeTextFile":false},"terminal":false,"_meta":{"github.com/copilot":{"events":["assistant.turn_start","assistant.idle","session.idle"]}}}}}`
const plainInit = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"fs":{"readTextFile":false,"writeTextFile":false},"terminal":false}}}`
const initOK = `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"sessionCapabilities":{"close":{}}},"agentInfo":{"name":"fakeagent","version":"1"}}}`

func newSession(h *acpHarness, init string) {
	h.t.Helper()
	h.send(init)
	h.expect(initOK)
	h.send(`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/w","mcpServers":[]}}`)
	h.expect(`{"jsonrpc":"2.0","id":2,"result":{"sessionId":"s1"}}`)
}

func prompt(id int, text string) string {
	b, _ := json.Marshal(text)
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"session/prompt","params":{"sessionId":"s1","prompt":[{"type":"text","text":%s}]}}`, id, b)
}

func chunk(kind, text string) string {
	b, _ := json.Marshal(text)
	return fmt.Sprintf(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":%q,"content":{"type":"text","text":%s}}}}`, kind, b)
}

func msg(text string) string { return chunk("agent_message_chunk", text) }

func tool(n int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call","toolCallId":"call_%d","title":"fake tool","kind":"execute","status":"completed"}}}`, n)
}

func stop(id int, reason string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"stopReason":%q,"usage":{"inputTokens":100,"outputTokens":10,"totalTokens":110}}}`, id, reason)
}

func event(typ, agent, data string) string {
	a := ""
	if agent != "" {
		a = fmt.Sprintf(`,"agentId":%q`, agent)
	}
	return fmt.Sprintf(`{"jsonrpc":"2.0","method":"github.com/copilot/sessionEvent","params":{"sessionId":"s1","type":%q%s,"data":%s}}`, typ, a, data)
}

const askFrame = `{"jsonrpc":"2.0","id":1,"method":"session/request_permission","params":{"sessionId":"s1","toolCall":{"toolCallId":"call_1","title":"fake tool","kind":"execute"},"options":[{"optionId":"allow","name":"Allow","kind":"allow_once"},{"optionId":"reject","name":"Reject","kind":"reject_once"}]}}`

func TestACPDefaultTurnWithoutCopilotEvents(t *testing.T) {
	h := startACP(t, ACPOptions{})
	newSession(h, plainInit)
	h.send(prompt(3, "hello"))
	h.expect(msg("fake: hello"), tool(1), stop(3, "end_turn"))
	h.quiet()
	if err := h.done(); err != nil {
		t.Fatal(err)
	}
}

func TestACPCopilotEventsWhenOptedIn(t *testing.T) {
	h := startACP(t, ACPOptions{})
	newSession(h, optIn)
	h.send(prompt(3, "hello"))
	h.expect(event("assistant.turn_start", "", `{}`), msg("fake: hello"), tool(1),
		event("assistant.idle", "", `{"aborted":false}`), event("session.idle", "", `{}`), stop(3, "end_turn"))
}

func TestACPSubagentEvents(t *testing.T) {
	h := startACP(t, ACPOptions{})
	newSession(h, optIn)
	h.send(prompt(3, "go [subagent]"))
	h.expect(event("assistant.turn_start", "", `{}`),
		event("assistant.turn_start", "sub1", `{}`), event("assistant.idle", "sub1", `{"aborted":false}`),
		msg("fake: go [subagent]"), tool(1),
		event("assistant.idle", "", `{"aborted":false}`), event("session.idle", "", `{}`), stop(3, "end_turn"))
}

func TestACPAsk(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		want   []string
	}{
		{"allow", `{"jsonrpc":"2.0","id":1,"result":{"outcome":{"outcome":"selected","optionId":"allow"}}}`,
			[]string{tool(1), stop(3, "end_turn")}},
		{"reject", `{"jsonrpc":"2.0","id":1,"result":{"outcome":{"outcome":"selected","optionId":"reject"}}}`,
			[]string{msg("rejected"), stop(3, "end_turn")}},
		{"cancelled", `{"jsonrpc":"2.0","id":1,"result":{"outcome":{"outcome":"cancelled"}}}`,
			[]string{stop(3, "cancelled")}},
		{"session/cancel", `{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"s1"}}`,
			[]string{stop(3, "cancelled")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := startACP(t, ACPOptions{})
			newSession(h, plainInit)
			h.send(prompt(3, "do it [ask]"))
			h.expect(msg("fake: do it [ask]"), askFrame)
			h.send(c.answer)
			h.expect(c.want...)
			h.quiet()
		})
	}
}

func TestACPSlow(t *testing.T) {
	h := startACP(t, ACPOptions{})
	newSession(h, plainInit)
	start := time.Now()
	h.send(prompt(3, "x [slow 150]"))
	h.expect(msg("fake: x [slow 150]"), tool(1), stop(3, "end_turn"))
	if d := time.Since(start); d < 150*time.Millisecond {
		t.Errorf("slow turn took %v, want >= 150ms", d)
	}
}

func TestACPHangUntilCancel(t *testing.T) {
	h := startACP(t, ACPOptions{})
	newSession(h, optIn)
	h.send(prompt(3, "[hang]"))
	h.expect(event("assistant.turn_start", "", `{}`), msg("fake: [hang]"))
	h.quiet()
	h.send(`{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"s1"}}`)
	h.expect(event("assistant.idle", "", `{"aborted":true}`), event("session.idle", "", `{}`), stop(3, "cancelled"))
}

func TestACPCrash(t *testing.T) {
	h := startACP(t, ACPOptions{})
	newSession(h, plainInit)
	h.send(prompt(3, "[crash]"))
	h.expect(msg("fake: [crash]"))
	select {
	case err := <-h.errc:
		if !errors.Is(err, ErrCrashed) {
			t.Fatalf("ServeACP = %v, want ErrCrashed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ServeACP did not return after the crash")
	}
	if c := h.exit.Load(); c != 3 {
		t.Fatalf("exit code %d, want 3", c)
	}
}

func TestACPError(t *testing.T) {
	h := startACP(t, ACPOptions{})
	newSession(h, plainInit)
	h.send(prompt(3, "[error]"))
	h.expect(`{"jsonrpc":"2.0","id":3,"error":{"code":-32603,"message":"fake: error"}}`)
	h.quiet()
}

func TestACPSteer(t *testing.T) {
	h := startACP(t, ACPOptions{})
	newSession(h, plainInit)
	h.send(prompt(3, "first [hang]"))
	h.expect(msg("fake: first [hang]"))
	h.send(prompt(4, "second"))
	h.expect(tool(1), msg("fake: second"), tool(2), stop(3, "end_turn"), stop(4, "end_turn"))
	h.quiet()
}

func TestACPCloseAndUnknown(t *testing.T) {
	h := startACP(t, ACPOptions{})
	newSession(h, plainInit)
	h.send(`{"jsonrpc":"2.0","id":3,"method":"session/close","params":{"sessionId":"s1"}}`)
	h.expect(`{"jsonrpc":"2.0","id":3,"result":{}}`)
	h.send(prompt(4, "after"))
	h.expect(`{"jsonrpc":"2.0","id":4,"error":{"code":-32602,"message":"unknown session s1"}}`)
	h.send(`{"jsonrpc":"2.0","id":"x","method":"fs/read_text_file","params":{}}`)
	h.expect(`{"jsonrpc":"2.0","id":"x","error":{"code":-32601,"message":"method not found: fs/read_text_file"}}`)
}

// A second process sharing the state dir can load (with replay) or, with
// --resume, resume (no replay) the first one's session.
func TestACPLoadReplaysResumeDoesNot(t *testing.T) {
	dir := t.TempDir()
	h := startACP(t, ACPOptions{StateDir: dir})
	newSession(h, plainInit)
	h.send(prompt(3, "hello"))
	h.expect(msg("fake: hello"), tool(1), stop(3, "end_turn"))
	if err := h.done(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "s1.json")); err != nil {
		t.Fatal(err)
	}

	load := `{"jsonrpc":"2.0","id":2,"method":"session/load","params":{"sessionId":"s1","cwd":"/w","mcpServers":[]}}`
	resume := strings.Replace(load, "session/load", "session/resume", 1)

	h2 := startACP(t, ACPOptions{StateDir: dir})
	h2.send(plainInit)
	h2.expect(initOK)
	h2.send(load)
	h2.expect(chunk("user_message_chunk", "hello"), msg("fake: hello"), `{"jsonrpc":"2.0","id":2,"result":{}}`)
	h2.send(strings.Replace(resume, `"id":2`, `"id":3`, 1))
	h2.expect(`{"jsonrpc":"2.0","id":3,"error":{"code":-32601,"message":"method not found: session/resume"}}`)

	h3 := startACP(t, ACPOptions{StateDir: dir, Resume: true, NoLoad: true})
	h3.send(plainInit)
	h3.expect(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":false,"sessionCapabilities":{"close":{},"resume":{}}},"agentInfo":{"name":"fakeagent","version":"1"}}}`)
	h3.send(resume)
	h3.expect(`{"jsonrpc":"2.0","id":2,"result":{}}`)
	h3.send(prompt(3, "again"))
	h3.expect(msg("fake: again"), tool(1), stop(3, "end_turn"))
	h3.send(strings.Replace(load, `"id":2`, `"id":4`, 1))
	h3.expect(`{"jsonrpc":"2.0","id":4,"error":{"code":-32601,"message":"method not found: session/load"}}`)
}

// Without --acp, Main is the TUI fake byte for byte.
func TestMainWithoutACPIsTheTUIFake(t *testing.T) {
	in, w := io.Pipe()
	var out bytes.Buffer
	done := make(chan int)
	go func() { done <- Main([]string{"a1"}, in, &out, io.Discard) }()
	io.WriteString(w, "!done refactor auth middleware\n")
	time.Sleep(300 * time.Millisecond)
	w.Close()
	if code := <-done; code != 0 {
		t.Fatalf("exit %d", code)
	}
	s := out.String()
	if !strings.HasPrefix(s, "fakeagent a1 ready (tool: fake)\n\n● Task: ") {
		t.Fatalf("start changed: %q", s)
	}
	_, after, ok := strings.Cut(s, "\x1b[2J\x1b[H")
	want := "● Task: refactor auth middleware\n" +
		"  ✓ read internal/auth/middleware.go · 142 lines\n" +
		"  ✓ grep \"func New\" internal/auth · 6 matches\n" +
		"  ✓ edit internal/auth/middleware.go · +23 −8\n" +
		"  ✓ edit internal/auth/token.go · +11 −4\n" +
		"  ✓ go build ./... · ok 1.2s\n" +
		"  ✓ go test ./internal/auth/ · ok · 14 tests\n" +
		"\n✔ Done: refactor auth middleware · 2 files · +34 −12\n> "
	if !ok || after != want {
		t.Fatalf("composed screen changed:\n got %q\nwant %q", after, want)
	}
}

func TestACPInvoke(t *testing.T) {
	h := startACP(t, ACPOptions{})
	newSession(h, plainInit)
	h.send(prompt(3, "x [invoke]"))
	in := `"name":"invoke_subagent","rawInput":{"Prompt":"help","Role":"helper","TypeName":"research"}`
	up := func(u string) string {
		return `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":` + u + `}}`
	}
	h.expect(msg("fake: x [invoke]"),
		up(`{"sessionUpdate":"tool_call","toolCallId":"call_1","title":"Run invoke_subagent","kind":"other","status":"in_progress",`+in+`}`),
		up(`{"sessionUpdate":"tool_call_update","toolCallId":"call_1","status":"completed",`+in+`}`),
		stop(3, "end_turn"))
}
