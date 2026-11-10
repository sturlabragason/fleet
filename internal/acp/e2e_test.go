package acp_test

// End-to-end: the real Client and Tracker against the fake ACP agent
// (fake.ServeACP) in-process over pipes. An external test package, because
// internal/fake imports internal/acp.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/acp"
	"github.com/sturlabragason/fleet/internal/fake"
)

// trackHandler feeds every callback into a Tracker and logs the resulting
// state, as the host does.
type trackHandler struct {
	tr      *acp.Tracker
	answer  func(acp.RequestPermissionParams) acp.RequestPermissionResult
	pending *atomic.Bool // a Prompt is in flight
	msgs    chan string  // agent message texts

	mu  sync.Mutex
	log []string
}

func (h *trackHandler) add(s string) {
	h.mu.Lock()
	h.log = append(h.log, s)
	h.mu.Unlock()
}

func (h *trackHandler) lines() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.log...)
}

func (h *trackHandler) SessionUpdate(n acp.SessionNotification) {
	st := h.tr.Update(n.Update)
	h.add(fmt.Sprintf("u:%s:%s -> %s", n.Update.Kind, n.Update.Text(), st.Status))
	if n.Update.Kind == acp.UpdateAgentMessage {
		select {
		case h.msgs <- n.Update.Text():
		default:
		}
	}
}

func (h *trackHandler) CopilotEvent(e acp.CopilotEvent) {
	st := h.tr.Copilot(e)
	h.add(fmt.Sprintf("c:%s/%s -> %s %s pending=%v", e.Type, e.AgentID, st.Status, st.Event, h.pending.Load()))
}

func (h *trackHandler) RequestPermission(ctx context.Context, p acp.RequestPermissionParams) (acp.RequestPermissionResult, error) {
	st := h.tr.PermissionAsked(p)
	h.add(fmt.Sprintf("perm -> %s %q", st.Status, st.WaitingFor))
	res := acp.CancelledOutcome()
	if h.answer != nil {
		res = h.answer(p)
	}
	st = h.tr.PermissionAnswered(p.ToolCall.ToolCallID)
	h.add(fmt.Sprintf("answered %s -> %s", res.Outcome.OptionID, st.Status))
	return res, nil
}

type e2e struct {
	t   *testing.T
	cl  *acp.Client
	tr  *acp.Tracker
	h   *trackHandler
	sid string
}

// startFake connects a Client to a fresh fake server on dir, then
// initializes. The server's return error closes the client's input, as a
// child exit would.
func startFake(t *testing.T, dir string, opt fake.ACPOptions, events bool, answer func(acp.RequestPermissionParams) acp.RequestPermissionResult) (*e2e, acp.InitializeResult) {
	t.Helper()
	cr, aw := io.Pipe() // agent -> client
	ar, cw := io.Pipe() // client -> agent
	opt.StateDir = dir
	opt.Exit = func(int) {}
	go func() {
		err := fake.ServeACP(ar, aw, opt)
		ar.CloseWithError(io.ErrClosedPipe)
		aw.CloseWithError(err) // nil: clean EOF
	}()
	tr := acp.NewTracker()
	h := &trackHandler{tr: tr, answer: answer, pending: &atomic.Bool{}, msgs: make(chan string, 64)}
	cl := acp.NewClient(cr, cw, h)
	t.Cleanup(func() {
		cl.Close()
		select {
		case <-cl.Done():
		case <-time.After(5 * time.Second):
			t.Error("client not done after Close")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := cl.Initialize(ctx, events)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return &e2e{t: t, cl: cl, tr: tr, h: h}, res
}

func (e *e2e) newSession() {
	e.t.Helper()
	id, err := e.cl.NewSession(context.Background(), e.t.TempDir(), nil)
	if err != nil {
		e.t.Fatalf("session/new: %v", err)
	}
	e.sid = id
	if st := e.tr.Ready(); st != (acp.State{Status: acp.StatusIdle, Event: "acp:ready"}) {
		e.t.Fatalf("ready after session/new: %+v", st)
	}
}

func (e *e2e) prompt(text string) (acp.StopReason, error, acp.State) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e.tr.PromptSent()
	e.h.pending.Store(true)
	r, err := e.cl.Prompt(ctx, e.sid, text)
	e.h.pending.Store(false)
	return r, err, e.tr.PromptDone(r, err)
}

func (e *e2e) logHas(want ...string) {
	e.t.Helper()
	got := strings.Join(e.h.lines(), "\n")
	for _, w := range want {
		if !strings.Contains(got, w) {
			e.t.Errorf("log lacks %q:\n%s", w, got)
		}
	}
}

func TestE2EDefaultTurn(t *testing.T) {
	e, _ := startFake(t, t.TempDir(), fake.ACPOptions{}, false, nil)
	e.newSession()
	r, err, st := e.prompt("hi")
	if err != nil || r != acp.StopEndTurn {
		t.Fatalf("prompt = %v, %v", r, err)
	}
	if st != (acp.State{Status: acp.StatusIdle, Event: "acp:end_turn"}) {
		t.Errorf("state %+v", st)
	}
	e.logHas("u:agent_message_chunk:fake: hi -> working", "u:tool_call: -> working")
	for _, l := range e.h.lines() {
		if strings.HasPrefix(l, "c:") {
			t.Errorf("copilot event without opt-in: %s", l)
		}
	}
}

func TestE2EAsk(t *testing.T) {
	for _, tc := range []struct {
		option, wantMsg string
		wantTool        bool
	}{
		{"allow", "", true},
		{"reject", "rejected", false},
	} {
		t.Run(tc.option, func(t *testing.T) {
			answer := func(acp.RequestPermissionParams) acp.RequestPermissionResult { return acp.Selected(tc.option) }
			e, _ := startFake(t, t.TempDir(), fake.ACPOptions{}, false, answer)
			e.newSession()
			r, err, st := e.prompt("do it [ask]")
			if err != nil || r != acp.StopEndTurn || st.Status != acp.StatusIdle {
				t.Fatalf("prompt = %v, %v, %+v", r, err, st)
			}
			e.logHas(`perm -> waiting "fake tool: Allow / Reject"`, "answered "+tc.option+" -> working")
			log := strings.Join(e.h.lines(), "\n")
			if got := strings.Contains(log, "u:tool_call:"); got != tc.wantTool {
				t.Errorf("tool call ran = %v, want %v:\n%s", got, tc.wantTool, log)
			}
			if tc.wantMsg != "" {
				e.logHas("u:agent_message_chunk:" + tc.wantMsg)
			}
		})
	}
}

func TestE2EHangCancel(t *testing.T) {
	e, _ := startFake(t, t.TempDir(), fake.ACPOptions{}, false, nil)
	e.newSession()
	type out struct {
		r   acp.StopReason
		err error
		st  acp.State
	}
	res := make(chan out, 1)
	go func() { r, err, st := e.prompt("wait [hang]"); res <- out{r, err, st} }()
	select {
	case <-e.h.msgs:
	case <-time.After(5 * time.Second):
		t.Fatal("turn never started")
	}
	if st := e.tr.State(); st.Status != acp.StatusWorking {
		t.Errorf("during hang: %+v", st)
	}
	if err := e.cl.Cancel(e.sid); err != nil {
		t.Fatal(err)
	}
	o := <-res
	if o.err != nil || o.r != acp.StopCancelled || o.st != (acp.State{Status: acp.StatusIdle, Event: "acp:cancelled"}) {
		t.Fatalf("after cancel: %v %v %+v", o.r, o.err, o.st)
	}
}

func TestE2ECrash(t *testing.T) {
	e, _ := startFake(t, t.TempDir(), fake.ACPOptions{}, false, nil)
	e.newSession()
	r, err, _ := e.prompt("die [crash]")
	if !errors.Is(err, acp.ErrClosed) {
		t.Fatalf("prompt = %v, %v; want ErrClosed", r, err)
	}
	select {
	case <-e.cl.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done not closed after crash")
	}
	if !errors.Is(e.cl.Err(), fake.ErrCrashed) {
		t.Errorf("Err = %v, want ErrCrashed", e.cl.Err())
	}
	if st := e.tr.Exited(e.cl.Err()); st != (acp.State{Status: acp.StatusError, Event: "acp:exited"}) {
		t.Errorf("state %+v", st)
	}
}

func TestE2ECopilotEvents(t *testing.T) {
	e, _ := startFake(t, t.TempDir(), fake.ACPOptions{}, true, nil)
	e.newSession()
	r, err, st := e.prompt("go [subagent]")
	if err != nil || r != acp.StopEndTurn || st.Status != acp.StatusIdle {
		t.Fatalf("prompt = %v, %v, %+v", r, err, st)
	}
	e.logHas(
		"c:assistant.turn_start/ -> working copilot:assistant.turn_start pending=true",
		// Subagent events change nothing: still working on the foreground turn_start.
		"c:assistant.turn_start/sub1 -> working copilot:assistant.turn_start pending=true",
		"c:assistant.idle/sub1 -> working copilot:assistant.turn_start pending=true",
		// Foreground idle arrives (and is dispatched) before the prompt response.
		"c:assistant.idle/ -> idle copilot:assistant.idle pending=true",
		"c:session.idle/ -> idle copilot:session.idle pending=true",
	)
}

func TestE2ELoadVersusResume(t *testing.T) {
	dir := t.TempDir()
	first, _ := startFake(t, dir, fake.ACPOptions{}, false, nil)
	first.newSession()
	if _, err, _ := first.prompt("remember me"); err != nil {
		t.Fatal(err)
	}
	if err := first.cl.CloseSession(context.Background(), first.sid); err != nil {
		t.Fatal(err)
	}
	first.cl.Close()
	ctx := context.Background()

	t.Run("load replays", func(t *testing.T) {
		e, res := startFake(t, dir, fake.ACPOptions{}, false, nil)
		if !res.AgentCapabilities.CanLoad() || res.AgentCapabilities.CanResume() {
			t.Fatalf("caps %+v", res.AgentCapabilities)
		}
		if err := e.cl.LoadSession(ctx, first.sid, t.TempDir(), nil); err != nil {
			t.Fatal(err)
		}
		// The replay was dispatched before LoadSession returned, and it does
		// not make a starting agent look busy.
		e.logHas("u:user_message_chunk:remember me -> starting", "u:agent_message_chunk:fake: remember me -> starting")
		if st := e.tr.Ready(); st.Status != acp.StatusIdle {
			t.Errorf("ready after load: %+v", st)
		}
		e.sid = first.sid
		if _, err, st := e.prompt("again"); err != nil || st.Status != acp.StatusIdle {
			t.Fatalf("prompt after load: %v %+v", err, st)
		}
	})
	t.Run("resume does not replay", func(t *testing.T) {
		e, res := startFake(t, dir, fake.ACPOptions{Resume: true}, false, nil)
		if !res.AgentCapabilities.CanResume() {
			t.Fatalf("caps %+v", res.AgentCapabilities)
		}
		if err := e.cl.ResumeSession(ctx, first.sid, t.TempDir(), nil); err != nil {
			t.Fatal(err)
		}
		if l := e.h.lines(); len(l) != 0 {
			t.Errorf("resume replayed: %v", l)
		}
		if st := e.tr.Ready(); st.Status != acp.StatusIdle {
			t.Errorf("ready after resume: %+v", st)
		}
		e.sid = first.sid
		if _, err, st := e.prompt("more"); err != nil || st.Status != acp.StatusIdle {
			t.Fatalf("prompt after resume: %v %+v", err, st)
		}
	})
	t.Run("unknown session fails", func(t *testing.T) {
		e, _ := startFake(t, dir, fake.ACPOptions{}, false, nil)
		var re *acp.RPCError
		if err := e.cl.LoadSession(ctx, "nope", t.TempDir(), nil); !errors.As(err, &re) {
			t.Errorf("load unknown = %v", err)
		}
	})
}
