package acphost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/acp"
	"github.com/sturlabragason/fleet/internal/status"
)

// stubTracker follows the brief's rules closely enough for host tests.
type stubTracker struct {
	s        trackState
	open     map[string]bool
	inflight int
}

func newStubTracker() *stubTracker {
	return &stubTracker{s: trackState{Status: "starting"}, open: map[string]bool{}}
}
func (t *stubTracker) set(st, ev string) trackState {
	if len(t.open) > 0 && st != "error" {
		return t.s
	}
	t.s = trackState{Status: st, Event: ev}
	return t.s
}
func (t *stubTracker) Ready() trackState {
	if t.s.Status == "starting" {
		t.s = trackState{Status: "idle", Event: "acp:ready"}
	}
	return t.s
}
func (t *stubTracker) PromptSent() trackState { t.inflight++; return t.set("working", "acp:prompt") }
func (t *stubTracker) PromptDone(r acp.StopReason, err error) trackState {
	if t.inflight--; t.inflight > 0 {
		return t.s
	}
	t.open = map[string]bool{}
	if err != nil {
		return t.set("error", "acp:prompt-error")
	}
	return t.set("idle", "acp:"+string(r))
}
func (t *stubTracker) Update(u acp.SessionUpdate) trackState {
	if t.s.Status == "idle" && u.Kind == acp.UpdateToolCall {
		return t.set("working", "acp:"+u.Kind)
	}
	return t.s
}
func (t *stubTracker) PermissionAsked(p acp.RequestPermissionParams) trackState {
	t.open[p.ToolCall.ToolCallID] = true
	t.s = trackState{Status: "waiting", WaitingFor: p.ToolCall.Title, Event: "acp:permission"}
	return t.s
}
func (t *stubTracker) PermissionAnswered(id string) trackState {
	delete(t.open, id)
	if len(t.open) == 0 {
		t.s = trackState{Status: "working", Event: "acp:answered"}
	}
	return t.s
}
func (t *stubTracker) Copilot(e acp.CopilotEvent) trackState {
	if e.AgentID == "" && e.Type == "session.idle" {
		return t.set("idle", "copilot:"+e.Type)
	}
	return t.s
}
func (t *stubTracker) Exited(err error) trackState {
	t.open = map[string]bool{}
	return t.set("error", "acp:exit")
}
func (t *stubTracker) State() trackState { return t.s }

// stubConn is a scripted agent: each prompt waits for a turn func the test
// hands it on turns.
type stubConn struct {
	h      handler
	caps   acp.AgentCapabilities
	turns  chan func(h handler) acp.StopReason
	done   chan struct{}
	err    error
	once   sync.Once
	mu     sync.Mutex
	calls  []string
	cancel chan struct{}
	gate   chan struct{} // NewSession waits for it when set
	usage  *acp.Usage    // every prompt's usage, when set
	newErr error         // NewSession fails with it, when set
}

func newStubConn(caps acp.AgentCapabilities) *stubConn {
	return &stubConn{caps: caps, turns: make(chan func(handler) acp.StopReason, 4), done: make(chan struct{}), cancel: make(chan struct{}, 4)}
}
func (c *stubConn) call(s string) {
	c.mu.Lock()
	c.calls = append(c.calls, s)
	c.mu.Unlock()
}
func (c *stubConn) Calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}
func (c *stubConn) Initialize(ctx context.Context, ev bool) (acp.InitializeResult, error) {
	c.call(fmt.Sprintf("initialize %v", ev))
	return acp.InitializeResult{ProtocolVersion: 1, AgentCapabilities: c.caps}, nil
}
func (c *stubConn) NewSession(ctx context.Context, cwd string, _ []acp.MCPServer) (string, error) {
	if c.gate != nil {
		<-c.gate
	}
	c.call("new " + cwd)
	if c.newErr != nil {
		return "", c.newErr
	}
	return "s1", nil
}
func (c *stubConn) LoadSession(ctx context.Context, id, cwd string, _ []acp.MCPServer) error {
	c.call("load " + id)
	c.h.SessionUpdate(acp.SessionNotification{SessionID: id, Update: chunk(acp.UpdateUserMessage, "old prompt")})
	c.h.SessionUpdate(acp.SessionNotification{SessionID: id, Update: chunk(acp.UpdateAgentMessage, "old answer")})
	return nil
}
func (c *stubConn) ResumeSession(ctx context.Context, id, cwd string, _ []acp.MCPServer) error {
	c.call("resume " + id)
	return nil
}
func (c *stubConn) Prompt(ctx context.Context, id, text string) (acp.PromptResult, error) {
	c.call("prompt " + text)
	select {
	case f := <-c.turns:
		if r := f(c.h); r != "ERR" {
			return acp.PromptResult{StopReason: r, Usage: c.usage}, nil
		}
		return acp.PromptResult{}, errors.New("rejected")
	case <-c.done:
		return acp.PromptResult{}, errors.New("closed")
	}
}
func (c *stubConn) Cancel(id string) error { c.call("cancel"); c.cancel <- struct{}{}; return nil }
func (c *stubConn) CloseSession(ctx context.Context, id string) error {
	c.call("close " + id)
	return nil
}
func (c *stubConn) Done() <-chan struct{} { return c.done }
func (c *stubConn) Err() error            { return c.err }
func (c *stubConn) Close() error {
	c.call("closeconn")
	c.once.Do(func() { close(c.done) })
	return nil
}
func (c *stubConn) crash(err error) {
	c.err = err
	c.once.Do(func() { close(c.done) })
}

func chunk(kind, text string) acp.SessionUpdate {
	b, _ := json.Marshal(acp.TextBlock(text))
	return acp.SessionUpdate{Kind: kind, Content: b}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type rig struct {
	t    *testing.T
	home string
	conn *stubConn
	out  *syncBuf
	errC chan error
	fin  chan struct{}
	stop context.CancelFunc
}

func startRig(t *testing.T, cfg Config, caps acp.AgentCapabilities) *rig {
	t.Helper()
	return startRigGated(t, cfg, caps, nil)
}

// startRigGated holds session/new until gate closes (nil: no wait).
func startRigGated(t *testing.T, cfg Config, caps acp.AgentCapabilities, gate chan struct{}) *rig {
	t.Helper()
	t.Setenv("FLEET_CONFIG", filepath.Join(t.TempDir(), "none.toml"))
	// unix socket paths are short-limited; keep home short.
	home, err := os.MkdirTemp("", "ah")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	r := &rig{t: t, home: home, conn: newStubConn(caps), out: &syncBuf{}, errC: make(chan error, 1), fin: make(chan struct{})}
	r.conn.gate = gate
	cfg.Home, cfg.Out = home, r.out
	if cfg.Name == "" {
		cfg.Name = "a1"
	}
	cfg.Argv = []string{"stub"}
	if cfg.Dir == "" {
		cfg.Dir = t.TempDir()
	}
	start := func(ctx context.Context, argv []string, dir string, env []string, stderr io.Writer, h handler) (agentConn, *exec.Cmd, error) {
		r.conn.h = h
		return r.conn, nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.stop = cancel
	go func() { r.errC <- run(ctx, cfg, start, newStubTracker()); close(r.fin) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.fin:
		case <-time.After(10 * time.Second):
		}
	})
	if gate != nil {
		r.waitFor(func() bool { _, err := Call(home, cfg.Name, Request{Op: OpState}, time.Second); return err == nil }, "socket")
		return r
	}
	r.waitFor(func() bool { return LastSession(TranscriptPath(home, cfg.Name)) != "" }, "session")
	return r
}

func (r *rig) call(req Request) (Response, error) {
	return Call(r.home, "a1", req, 5*time.Second)
}

func (r *rig) waitFor(cond func() bool, what string) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatalf("timed out waiting for %s; out:\n%s", what, r.out.String())
}

func (r *rig) status() status.State {
	b, _ := os.ReadFile(filepath.Join(status.StateDir(r.home), "a1.json"))
	var s status.State
	_ = json.Unmarshal(b, &s)
	return s
}

func (r *rig) waitStatus(want string) {
	r.t.Helper()
	r.waitFor(func() bool { return string(r.status().Status) == want }, "status "+want)
}

func TestPromptTurnWritesStatusTranscriptAndPane(t *testing.T) {
	r := startRig(t, Config{}, acp.AgentCapabilities{})
	if s := r.status(); s.Status != "idle" || s.Event != "acp:ready" {
		t.Fatalf("status before prompt = %+v", s)
	}
	if _, err := r.call(Request{Op: OpPrompt, Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	r.waitStatus("working")
	r.conn.turns <- func(h handler) acp.StopReason {
		h.SessionUpdate(acp.SessionNotification{Update: chunk(acp.UpdateAgentMessage, "hi ")})
		h.SessionUpdate(acp.SessionNotification{Update: chunk(acp.UpdateAgentMessage, "there")})
		h.SessionUpdate(acp.SessionNotification{Update: acp.SessionUpdate{Kind: acp.UpdateToolCall, ToolCallID: "t1", Title: "Read file", Status: "pending"}})
		h.SessionUpdate(acp.SessionNotification{Update: acp.SessionUpdate{Kind: acp.UpdateToolCallUpdate, ToolCallID: "t1", Status: "completed"}})
		return acp.StopEndTurn
	}
	r.waitStatus("idle")
	resp, err := r.call(Request{Op: OpState})
	if err != nil || resp.Status != "idle" || resp.SessionID != "s1" {
		t.Fatalf("state = %+v, %v", resp, err)
	}
	var kinds []string
	var agent string
	for _, e := range Tail(TranscriptPath(r.home, "a1"), 100) {
		if e.Kind == KindStatus {
			continue
		}
		kinds = append(kinds, e.Kind)
		if e.Kind == KindAgent {
			agent = e.Text
		}
	}
	if got := strings.Join(kinds, ","); got != "session,user,agent,tool,tool" {
		t.Fatalf("transcript kinds = %s", got)
	}
	if agent != "hi there" {
		t.Fatalf("agent text = %q (chunks not joined)", agent)
	}
	out := r.out.String()
	for _, want := range []string{"> hello", "hi there", "* Read file (completed)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("pane lacks %q:\n%s", want, out)
		}
	}
	if got := LastLine(TranscriptPath(r.home, "a1")); got != "* Read file" {
		t.Fatalf("LastLine = %q", got)
	}
}

func TestPermissionAnswerAndCancel(t *testing.T) {
	r := startRig(t, Config{Prompt: "go"}, acp.AgentCapabilities{})
	r.waitStatus("working")
	if _, err := r.call(Request{Op: OpAnswer, Option: 1}); err == nil {
		t.Fatal("answer with no open question should fail")
	}
	results := make(chan acp.RequestPermissionResult, 2)
	ask := func(h handler) acp.StopReason {
		res, _ := h.RequestPermission(context.Background(), acp.RequestPermissionParams{
			ToolCall: acp.ToolCallRef{ToolCallID: "t9", Title: "Run ls"},
			Options:  []acp.PermissionOption{{OptionID: "a", Name: "Allow", Kind: "allow_once"}, {OptionID: "r", Name: "Reject", Kind: "reject_once"}},
		})
		results <- res
		if res.Outcome.Outcome == "cancelled" {
			return acp.StopCancelled
		}
		return acp.StopEndTurn
	}
	r.conn.turns <- ask
	r.waitStatus("waiting")
	resp, err := r.call(Request{Op: OpState})
	if err != nil || resp.Question == nil || resp.Question.Title != "Run ls" || len(resp.Question.Options) != 2 || resp.Question.Options[1].Name != "Reject" {
		t.Fatalf("state = %+v, %v", resp, err)
	}
	if _, err := r.call(Request{Op: OpAnswer, Option: 3}); err == nil {
		t.Fatal("option 3 should be out of range")
	}
	if _, err := r.call(Request{Op: OpAnswer, Option: 2}); err != nil {
		t.Fatal(err)
	}
	if res := <-results; res.Outcome.OptionID != "r" {
		t.Fatalf("answer = %+v", res)
	}
	r.waitStatus("idle")
	if !strings.Contains(r.out.String(), "2) Reject") || !strings.Contains(r.out.String(), "-> Reject") {
		t.Fatalf("pane:\n%s", r.out.String())
	}

	// A second question, cancelled.
	if _, err := r.call(Request{Op: OpPrompt, Text: "again"}); err != nil {
		t.Fatal(err)
	}
	r.conn.turns <- ask
	r.waitStatus("waiting")
	if _, err := r.call(Request{Op: OpCancel}); err != nil {
		t.Fatal(err)
	}
	if res := <-results; res.Outcome.Outcome != "cancelled" {
		t.Fatalf("cancel answer = %+v", res)
	}
	r.waitStatus("idle")
	tail := Tail(TranscriptPath(r.home, "a1"), 100)
	var answers []string
	for _, e := range tail {
		if e.Kind == KindAnswer {
			answers = append(answers, e.Text)
		}
	}
	if strings.Join(answers, ",") != "Reject,cancelled" {
		t.Fatalf("answers = %v", answers)
	}
}

func TestTypedDigitAnswersAndLinePrompts(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	r := startRig(t, Config{In: pr}, acp.AgentCapabilities{})
	fmt.Fprintln(pw, "do it")
	r.waitStatus("working")
	got := make(chan string, 1)
	r.conn.turns <- func(h handler) acp.StopReason {
		res, _ := h.RequestPermission(context.Background(), acp.RequestPermissionParams{
			ToolCall: acp.ToolCallRef{ToolCallID: "t1", Title: "Edit"},
			Options:  []acp.PermissionOption{{OptionID: "a", Name: "Allow"}, {OptionID: "r", Name: "Reject"}},
		})
		got <- res.Outcome.OptionID
		return acp.StopEndTurn
	}
	r.waitStatus("waiting")
	fmt.Fprintln(pw, "1")
	if id := <-got; id != "a" {
		t.Fatalf("typed 1 answered %q", id)
	}
	r.waitStatus("idle")
	if calls := strings.Join(r.conn.Calls(), "|"); !strings.Contains(calls, "prompt do it") || strings.Contains(calls, "prompt 1") {
		t.Fatalf("calls = %s", calls)
	}
}

func TestStopClosesSessionAndExitsZero(t *testing.T) {
	r := startRig(t, Config{}, acp.AgentCapabilities{SessionCapabilities: acp.SessionCapabilities{Close: json.RawMessage(`{}`)}})
	if _, err := r.call(Request{Op: OpStop}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-r.errC:
		if err != nil {
			t.Fatalf("run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host did not stop")
	}
	calls := strings.Join(r.conn.Calls(), "|")
	if !strings.Contains(calls, "close s1|closeconn") {
		t.Fatalf("calls = %s", calls)
	}
	if _, err := os.Stat(SocketPath(r.home, "a1")); !os.IsNotExist(err) {
		t.Fatalf("socket left behind: %v", err)
	}
	if _, err := r.call(Request{Op: OpState}); !errors.Is(err, ErrNoHost) {
		t.Fatalf("call after stop = %v", err)
	}
}

func TestContextCancelStops(t *testing.T) {
	r := startRig(t, Config{}, acp.AgentCapabilities{})
	r.stop()
	select {
	case err := <-r.errC:
		if err != nil {
			t.Fatalf("run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host did not stop")
	}
	if strings.Contains(strings.Join(r.conn.Calls(), "|"), "close s1") {
		t.Fatal("closed a session the agent doesn't offer close for")
	}
}

func TestChildExitIsError(t *testing.T) {
	r := startRig(t, Config{Prompt: "x"}, acp.AgentCapabilities{})
	r.waitStatus("working")
	r.conn.crash(errors.New("exit status 3"))
	select {
	case err := <-r.errC:
		if err == nil || !strings.Contains(err.Error(), "exit status 3") {
			t.Fatalf("run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host did not exit")
	}
	if s := r.status(); s.Status != "error" {
		t.Fatalf("status = %q", s.Status)
	}
	tail := Tail(TranscriptPath(r.home, "a1"), 3)
	found := false
	for _, e := range tail {
		found = found || (e.Kind == KindError && strings.Contains(e.Text, "exit status 3"))
	}
	if !found {
		t.Fatalf("no error entry in %+v", tail)
	}
}

func TestSessionResumeOrLoad(t *testing.T) {
	r := startRig(t, Config{SessionID: "old"}, acp.AgentCapabilities{SessionCapabilities: acp.SessionCapabilities{Resume: json.RawMessage(`{}`)}})
	if c := r.conn.Calls(); len(c) < 2 || c[1] != "resume old" {
		t.Fatalf("calls = %v", c)
	}
	if LastSession(TranscriptPath(r.home, "a1")) != "old" {
		t.Fatal("session entry")
	}

	l := startRig(t, Config{SessionID: "old"}, acp.AgentCapabilities{LoadSession: true})
	if c := l.conn.Calls(); len(c) < 2 || c[1] != "load old" {
		t.Fatalf("calls = %v", c)
	}
	out := l.out.String()
	if !strings.Contains(out, "> old prompt") || !strings.Contains(out, "old answer") {
		t.Fatalf("replay not drawn:\n%s", out)
	}
	for _, e := range Tail(TranscriptPath(l.home, "a1"), 100) {
		if e.Kind == KindAgent || e.Kind == KindUser {
			t.Fatalf("replay recorded: %+v", e)
		}
	}
	if s := l.status(); s.Status != "idle" {
		t.Fatalf("replay moved status to %q", s.Status)
	}
}

func TestSecondHostRefusedStaleSocketRemoved(t *testing.T) {
	r := startRig(t, Config{}, acp.AgentCapabilities{})
	before, _ := os.ReadFile(TranscriptPath(r.home, "a1"))
	err := run(context.Background(), Config{Home: r.home, Name: "a1", Argv: []string{"stub"}}, nil, newStubTracker())
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second host = %v", err)
	}
	if after, _ := os.ReadFile(TranscriptPath(r.home, "a1")); !bytes.Equal(before, after) {
		t.Fatal("second host wrote to the live transcript")
	}
	p := filepath.Join(r.home, "x.sock")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := listen(p)
	if err != nil {
		t.Fatalf("stale socket not replaced: %v", err)
	}
	defer ln.Close()
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v", st.Mode().Perm())
	}
}

func (r *rig) kinds(kind string) []Entry {
	var out []Entry
	for _, e := range Tail(TranscriptPath(r.home, "a1"), 1000) {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func TestSteerErrorKeepsQuestionTurnEndCancelsIt(t *testing.T) {
	r := startRig(t, Config{Prompt: "go"}, acp.AgentCapabilities{})
	r.waitStatus("working")
	results := make(chan acp.RequestPermissionResult, 1)
	release := make(chan struct{})
	r.conn.turns <- func(h handler) acp.StopReason {
		go func() {
			res, _ := h.RequestPermission(context.Background(), acp.RequestPermissionParams{
				ToolCall: acp.ToolCallRef{ToolCallID: "t1", Title: "Run"},
				Options:  []acp.PermissionOption{{OptionID: "a", Name: "Allow"}},
			})
			results <- res
		}()
		<-release
		return acp.StopEndTurn
	}
	r.waitStatus("waiting")
	if _, err := r.call(Request{Op: OpPrompt, Text: "steer"}); err != nil {
		t.Fatal(err)
	}
	r.conn.turns <- func(handler) acp.StopReason { return "ERR" }
	r.waitFor(func() bool { return len(r.kinds(KindError)) > 0 }, "prompt error entry")
	resp, _ := r.call(Request{Op: OpState})
	if resp.Status != "waiting" || resp.Question == nil || r.status().Status != "waiting" {
		t.Fatalf("after rejected steer: %+v, file %+v", resp, r.status())
	}
	close(release) // the turn ends with the question still open
	select {
	case res := <-results:
		if res.Outcome.Outcome != "cancelled" {
			t.Fatalf("question outcome = %+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("permission goroutine not released at turn end")
	}
	r.waitStatus("idle")
	if resp, _ := r.call(Request{Op: OpState}); resp.Question != nil || resp.Status != "idle" {
		t.Fatalf("after turn end: %+v", resp)
	}
}

func TestPromptBeforeReadyIsQueued(t *testing.T) {
	gate := make(chan struct{})
	r := startRigGated(t, Config{}, acp.AgentCapabilities{}, gate)
	if _, err := r.call(Request{Op: OpPrompt, Text: "early"}); err != nil {
		t.Fatalf("prompt while starting: %v", err)
	}
	if st := r.status().Status; st != "starting" {
		t.Fatalf("status = %q", st)
	}
	close(gate)
	r.waitFor(func() bool { return strings.Contains(strings.Join(r.conn.Calls(), "|"), "prompt early") }, "queued prompt sent")
	r.waitStatus("working")
	var order []string
	for _, e := range Tail(TranscriptPath(r.home, "a1"), 100) {
		if e.Kind == KindSession || e.Kind == KindUser {
			order = append(order, e.Kind)
		}
	}
	if strings.Join(order, ",") != "session,user" {
		t.Fatalf("order = %v", order)
	}
}

func TestFirstPromptNoIdleBlipAndPromptSentHook(t *testing.T) {
	sent := make(chan struct{}, 2)
	r := startRig(t, Config{Prompt: "go", OnPromptSent: func() { sent <- struct{}{} }}, acp.AgentCapabilities{})
	r.waitStatus("working")
	var statuses []string
	for _, e := range r.kinds(KindStatus) {
		statuses = append(statuses, e.Status)
	}
	if strings.Join(statuses, ",") != "starting,working" {
		t.Fatalf("statuses = %v", statuses)
	}
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("OnPromptSent not called")
	}
	if len(sent) != 0 {
		t.Fatal("OnPromptSent called twice")
	}
}

func TestListenRefusesLongPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), strings.Repeat("x", 120)+".sock")
	if _, err := listen(p); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("listen = %v", err)
	}
}

// A turn's usage and agy's subagent calls go into the transcript, where
// fleet reads spend and subagent dots for a tool that keeps no log of its
// own (DESIGN §7d).
func TestUsageAndSubagentInTranscript(t *testing.T) {
	r := startRig(t, Config{}, acp.AgentCapabilities{})
	r.conn.usage = &acp.Usage{InputTokens: 4135, OutputTokens: 102, CachedReadTokens: 41080, TotalTokens: 45317}
	if _, err := r.call(Request{Op: OpPrompt, Text: "go"}); err != nil {
		t.Fatal(err)
	}
	r.waitStatus("working")
	in := json.RawMessage(`{"Prompt":"say OK","Role":"OK Responder","TypeName":"research"}`)
	r.conn.turns <- func(h handler) acp.StopReason {
		h.SessionUpdate(acp.SessionNotification{Update: acp.SessionUpdate{Kind: acp.UpdateToolCall, ToolCallID: "c1", Name: acp.SubagentTool, RawInput: in, Title: "Run invoke_subagent", Status: "in_progress"}})
		h.SessionUpdate(acp.SessionNotification{Update: acp.SessionUpdate{Kind: acp.UpdateToolCallUpdate, ToolCallID: "c1", Name: acp.SubagentTool, Status: "completed"}})
		return acp.StopEndTurn
	}
	r.waitStatus("idle")
	var subs []string
	var u *acp.Usage
	r.waitFor(func() bool {
		subs, u = nil, nil
		for _, e := range Tail(TranscriptPath(r.home, "a1"), 100) {
			if e.Kind == KindTool && e.Sub != "" {
				subs = append(subs, e.Sub+" "+e.Status)
			}
			if e.Kind == KindUsage {
				u = e.Usage
			}
		}
		return u != nil
	}, "usage entry")
	if *u != *r.conn.usage {
		t.Errorf("usage %+v", u)
	}
	if got := strings.Join(subs, ","); got != "OK Responder in_progress,OK Responder completed" {
		t.Errorf("subagent entries %q", got)
	}
}

// A host that ends before its session is ready keeps the first prompt and
// every prompt queued since in the prompt file for the next host, and says
// so in the transcript (the queued prompts used to be lost).
func TestUnsentPromptsKeptWhenSetupFails(t *testing.T) {
	for _, how := range []string{"child exit", "session error"} {
		t.Run(how, func(t *testing.T) {
			gate := make(chan struct{})
			r := startRigGated(t, Config{Prompt: "first goal"}, acp.AgentCapabilities{}, gate)
			if _, err := r.call(Request{Op: OpPrompt, Text: "queued one"}); err != nil {
				t.Fatal(err)
			}
			if how == "child exit" {
				r.conn.crash(errors.New("exit status 1"))
			} else {
				r.conn.newErr = errors.New("auth required")
				close(gate)
			}
			select {
			case err := <-r.errC:
				if err == nil {
					t.Fatal("run returned nil")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("host did not end")
			}
			if how == "child exit" {
				close(gate)
			}
			b, err := os.ReadFile(PromptPath(r.home, "a1"))
			if err != nil || string(b) != "first goal\n\nqueued one" {
				t.Fatalf("prompt file %q, %v", b, err)
			}
			var note string
			for _, e := range Tail(TranscriptPath(r.home, "a1"), 100) {
				if e.Kind == KindError && strings.Contains(e.Text, "not sent") {
					note = e.Text
				}
			}
			if !strings.Contains(note, "2 prompt(s) not sent") || !strings.Contains(note, "> queued one") {
				t.Fatalf("transcript note %q", note)
			}
		})
	}
}
