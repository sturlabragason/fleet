package acphost

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sturlabragason/fleet/internal/acp"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/status"
)

// Config is one host's agent: who it is, what to start and where.
type Config struct {
	Home          string // fleet state dir; "" means logx.Home()
	Name          string
	Dir           string // the agent's working dir; "" means the current dir
	Argv          []string
	Env           []string
	SessionID     string // resume or load this session instead of starting one
	Prompt        string // sent once the session is ready
	OnPromptSent  func() // called once Prompt has been handed to the agent
	CopilotEvents bool
	MCP           []acp.MCPServer
	Out           io.Writer // the pane; nil draws nothing
	In            io.Reader // typed lines; nil reads nothing
}

// agentConn is the part of *acp.Client the host uses.
type agentConn interface {
	Initialize(ctx context.Context, copilotEvents bool) (acp.InitializeResult, error)
	NewSession(ctx context.Context, cwd string, mcp []acp.MCPServer) (string, error)
	LoadSession(ctx context.Context, id, cwd string, mcp []acp.MCPServer) error
	ResumeSession(ctx context.Context, id, cwd string, mcp []acp.MCPServer) error
	Prompt(ctx context.Context, id, text string) (acp.PromptResult, error)
	Cancel(id string) error
	CloseSession(ctx context.Context, id string) error
	Done() <-chan struct{}
	Err() error
	Close() error
}

// handler is acp.Handler: what the client calls back.
type handler interface {
	SessionUpdate(n acp.SessionNotification)
	RequestPermission(ctx context.Context, p acp.RequestPermissionParams) (acp.RequestPermissionResult, error)
	CopilotEvent(e acp.CopilotEvent)
}

// starter is acp.Start.
type starter func(ctx context.Context, argv []string, dir string, env []string, stderr io.Writer, h handler) (agentConn, *exec.Cmd, error)

// trackState is acp.State.
type trackState struct {
	Status     string
	WaitingFor string
	Event      string
}

// stateTracker is *acp.Tracker: the status state machine.
type stateTracker interface {
	Ready() trackState
	PromptSent() trackState
	PromptDone(r acp.StopReason, err error) trackState
	Update(u acp.SessionUpdate) trackState
	PermissionAsked(p acp.RequestPermissionParams) trackState
	PermissionAnswered(toolCallID string) trackState
	Copilot(e acp.CopilotEvent) trackState
	Exited(err error) trackState
	State() trackState
}

// Timeouts for shutting down: how long session/close and the child's own
// exit may take before the child is killed.
var (
	closeTimeout = 3 * time.Second
	exitTimeout  = 3 * time.Second
)

// Run hosts one ACP agent until it is stopped (OpStop, ctx cancelled: nil)
// or its child exits (an error).
func Run(ctx context.Context, cfg Config) error {
	return run(ctx, cfg, defaultStart, newTracker())
}

type question struct {
	p      acp.RequestPermissionParams
	title  string
	answer chan acp.RequestPermissionResult
}

type host struct {
	cfg   Config
	home  string
	tr    stateTracker
	tx    *transcript
	out   io.Writer
	bg    context.Context
	stopC chan struct{}
	once  sync.Once

	mu        sync.Mutex
	conn      agentConn
	sid       string
	caps      acp.AgentCapabilities
	ready     bool
	stopping  bool
	exited    bool
	state     trackState
	questions []*question
	replaying bool
	pending   []string          // prompts that came before the session was ready
	inflight  int               // prompts sent and not yet answered
	titles    map[string]string // tool call id → title
	subs      map[string]string // tool call id → the subagent it starts
	buf       struct {
		kind   string
		replay bool
		text   strings.Builder
	}
}

func run(ctx context.Context, cfg Config, start starter, tr stateTracker) error {
	if cfg.Name == "" || len(cfg.Argv) == 0 {
		return errors.New("acp-host: need a name and a command")
	}
	if cfg.Home == "" {
		cfg.Home = logx.Home()
	}
	if cfg.Dir == "" {
		cfg.Dir, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(cfg.Dir); err == nil {
		cfg.Dir = abs
	}
	log := logx.For("acphost").With("agent", cfg.Name)
	// The socket first: a second host for a live agent must fail before it
	// touches the status file or the transcript.
	ln, err := listen(SocketPath(cfg.Home, cfg.Name))
	if err != nil {
		return err
	}
	defer ln.Close()
	tx, err := openTranscript(TranscriptPath(cfg.Home, cfg.Name))
	if err != nil {
		return err
	}
	defer tx.close()
	bg, cancelBg := context.WithCancel(context.Background())
	defer cancelBg()
	h := &host{cfg: cfg, home: cfg.Home, tr: tr, tx: tx, out: cfg.Out, bg: bg,
		stopC: make(chan struct{}), titles: map[string]string{}, subs: map[string]string{}}
	if h.out == nil {
		h.out = io.Discard
	}
	h.mu.Lock()
	h.apply(tr.State())
	h.mu.Unlock()

	go h.serve(ln)

	stderr, err := os.OpenFile(filepath.Join(Dir(cfg.Home), cfg.Name+".stderr"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer stderr.Close()

	conn, cmd, err := start(bg, cfg.Argv, cfg.Dir, cfg.Env, stderr, h)
	if err != nil {
		h.fail(err)
		return fmt.Errorf("acp-host: start %s: %w", cfg.Argv[0], err)
	}
	h.mu.Lock()
	h.conn = conn
	h.mu.Unlock()
	exitC := make(chan error, 1)
	exited := make(chan struct{})
	go func() {
		<-conn.Done() // acp.Start waits for the child; Err says how it ended
		exitC <- conn.Err()
		close(exited)
	}()
	setupC := make(chan error, 1)
	go func() { setupC <- h.setup() }()
	if cfg.In != nil {
		go h.input(cfg.In)
	}

	for {
		select {
		case <-ctx.Done():
			log.Info("stop", "why", "signal")
			h.shutdown(cmd, exited)
			return nil
		case <-h.stopC:
			log.Info("stop", "why", "request")
			h.shutdown(cmd, exited)
			return nil
		case err := <-setupC:
			if err == nil {
				continue
			}
			select {
			case werr := <-exitC: // the child died during setup
				return h.childExited(werr)
			case <-time.After(200 * time.Millisecond):
			}
			h.fail(err)
			h.shutdown(cmd, exited)
			return fmt.Errorf("acp-host: %w", err)
		case werr := <-exitC:
			return h.childExited(werr)
		}
	}
}

// setup initializes the agent and starts, resumes or loads the session,
// then sends the first prompt.
func (h *host) setup() error {
	c := h.conn
	res, err := c.Initialize(h.bg, h.cfg.CopilotEvents)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	caps := res.AgentCapabilities
	id, how := h.cfg.SessionID, ""
	switch {
	case id != "" && caps.CanResume():
		how = "resumed"
		err = c.ResumeSession(h.bg, id, h.cfg.Dir, h.cfg.MCP)
	case id != "" && caps.CanLoad():
		how = "loaded"
		h.mu.Lock()
		h.replaying = true
		h.mu.Unlock()
		err = c.LoadSession(h.bg, id, h.cfg.Dir, h.cfg.MCP)
		h.mu.Lock()
		h.flush()
		h.replaying = false
		h.mu.Unlock()
	default:
		if id != "" {
			h.mu.Lock()
			h.emit(Entry{Kind: KindError, Text: "agent can't resume or load session " + id + "; starting a new one"}, true)
			h.mu.Unlock()
		}
		how = "new"
		id, err = c.NewSession(h.bg, h.cfg.Dir, h.cfg.MCP)
	}
	if err != nil {
		return fmt.Errorf("session (%s): %w", how, err)
	}
	h.mu.Lock()
	h.sid, h.caps, h.ready = id, caps, true
	h.emit(Entry{Kind: KindSession, SessionID: id, Text: how}, true)
	queue := h.pending
	h.pending = nil
	if h.cfg.Prompt != "" {
		queue = append([]string{h.cfg.Prompt}, queue...)
	}
	if len(queue) == 0 {
		h.apply(h.tr.Ready())
	}
	for _, text := range queue { // no idle blip: straight from starting to working
		h.send(text)
	}
	h.mu.Unlock()
	if h.cfg.Prompt != "" && h.cfg.OnPromptSent != nil {
		h.cfg.OnPromptSent()
	}
	return nil
}

// apply records a tracker state when it differs from the last one. Caller
// holds mu.
func (h *host) apply(s trackState) {
	if s == h.state {
		return
	}
	if s.Status != "working" {
		h.flush()
	}
	h.state = s
	if err := status.WriteState(status.StateDir(h.home), h.cfg.Name, status.State{
		Status: status.Status(s.Status), WaitingFor: s.WaitingFor, Event: s.Event, At: time.Now(),
	}); err != nil {
		logx.For("acphost").Warn("write state", "agent", h.cfg.Name, "err", err)
	}
	h.emit(Entry{Kind: KindStatus, Status: s.Status, Text: s.Event}, true)
}

// emit draws e and, when record is set, appends it to the transcript.
// Caller holds mu.
func (h *host) emit(e Entry, record bool) {
	if record {
		if err := h.tx.add(e); err != nil {
			logx.For("acphost").Warn("transcript", "agent", h.cfg.Name, "err", err)
		}
	}
	if s := render(e); s != "" {
		fmt.Fprintln(h.out, s)
	}
}

// flush ends the message being collected from chunks. Caller holds mu.
func (h *host) flush() {
	if h.buf.kind == "" {
		return
	}
	e := Entry{Kind: h.buf.kind, Text: h.buf.text.String()}
	replay := h.buf.replay
	h.buf.kind = ""
	h.buf.text.Reset()
	if strings.TrimSpace(e.Text) == "" {
		return
	}
	h.emit(e, !replay)
}

func (h *host) collect(kind, text string) {
	if h.buf.kind != kind || h.buf.replay != h.replaying {
		h.flush()
		h.buf.kind, h.buf.replay = kind, h.replaying
	}
	h.buf.text.WriteString(text)
}

// SessionUpdate is acp.Handler: collect message chunks, record tool calls
// and feed the tracker. A session/load replay is drawn but not recorded
// (the transcript already has it) and doesn't move the status.
func (h *host) SessionUpdate(n acp.SessionNotification) {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := n.Update
	switch u.Kind {
	case acp.UpdateAgentMessage:
		h.collect(KindAgent, u.Text())
	case acp.UpdateAgentThought:
		h.collect(KindThought, u.Text())
	case acp.UpdateUserMessage:
		if !h.replaying {
			return // fleet records its own prompts
		}
		h.collect(KindUser, u.Text())
	case acp.UpdateToolCall, acp.UpdateToolCallUpdate:
		h.flush()
		title := u.Title
		if title != "" {
			h.titles[u.ToolCallID] = title
		} else {
			title = h.titles[u.ToolCallID]
		}
		sub := h.subs[u.ToolCallID]
		if s, ok := u.Subagent(); ok && sub == "" {
			sub = s
			h.subs[u.ToolCallID] = s
		}
		if u.Kind == acp.UpdateToolCall || u.Status != "" || u.Title != "" {
			if title == "" {
				title = u.ToolCallID
			}
			h.emit(Entry{Kind: KindTool, Title: title, Status: u.Status, ToolID: u.ToolCallID, Sub: sub}, !h.replaying)
		}
	}
	if !h.replaying {
		h.apply(h.tr.Update(u))
	}
}

// CopilotEvent is acp.Handler.
func (h *host) CopilotEvent(e acp.CopilotEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.apply(h.tr.Copilot(e))
}

// RequestPermission is acp.Handler: open a question and block until it is
// answered (OpAnswer, a typed digit), cancelled (OpCancel, stop) or the
// client closes.
func (h *host) RequestPermission(ctx context.Context, p acp.RequestPermissionParams) (acp.RequestPermissionResult, error) {
	h.mu.Lock()
	h.flush()
	title := p.ToolCall.Title
	if title == "" {
		title = h.titles[p.ToolCall.ToolCallID]
	}
	if title == "" {
		title = "permission"
	}
	q := &question{p: p, title: title, answer: make(chan acp.RequestPermissionResult, 1)}
	if h.stopping {
		h.mu.Unlock()
		return acp.CancelledOutcome(), nil
	}
	h.questions = append(h.questions, q)
	h.apply(h.tr.PermissionAsked(p))
	h.emit(Entry{Kind: KindPermission, Title: title, ToolID: p.ToolCall.ToolCallID, Options: options(p)}, true)
	h.mu.Unlock()
	select {
	case r := <-q.answer:
		return r, nil
	case <-ctx.Done():
		h.mu.Lock()
		h.resolve(q, acp.CancelledOutcome(), "cancelled")
		h.mu.Unlock()
		return acp.CancelledOutcome(), ctx.Err()
	}
}

func options(p acp.RequestPermissionParams) []Option {
	out := make([]Option, len(p.Options))
	for i, o := range p.Options {
		out[i] = Option{ID: o.OptionID, Name: o.Name, Kind: o.Kind}
	}
	return out
}

// resolve answers q once: it leaves the open questions, the tracker and
// transcript hear of it and RequestPermission returns r. Caller holds mu.
func (h *host) resolve(q *question, r acp.RequestPermissionResult, label string) {
	if h.dropQuestion(q, r, label) {
		h.apply(h.tr.PermissionAnswered(q.p.ToolCall.ToolCallID))
	}
}

// dropQuestion is resolve without writing the resulting state, for a
// caller that writes the state it ends in. It reports whether q was open.
func (h *host) dropQuestion(q *question, r acp.RequestPermissionResult, label string) bool {
	i := -1
	for j, o := range h.questions {
		if o == q {
			i = j
		}
	}
	if i < 0 {
		return false
	}
	h.questions = append(h.questions[:i], h.questions[i+1:]...)
	q.answer <- r
	h.emit(Entry{Kind: KindAnswer, Text: label, ToolID: q.p.ToolCall.ToolCallID}, true)
	h.tr.PermissionAnswered(q.p.ToolCall.ToolCallID)
	return true
}

// answer answers the oldest open question by 1-based option number or by
// option id.
func (h *host) answer(n int, id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.questions) == 0 {
		return errors.New("no open question")
	}
	q := h.questions[0]
	opts := q.p.Options
	var pick *acp.PermissionOption
	switch {
	case id != "":
		for i := range opts {
			if opts[i].OptionID == id {
				pick = &opts[i]
			}
		}
		if pick == nil {
			return fmt.Errorf("no option %q", id)
		}
	case n >= 1 && n <= len(opts):
		pick = &opts[n-1]
	default:
		return fmt.Errorf("option %d out of range 1-%d", n, len(opts))
	}
	h.resolve(q, acp.Selected(pick.OptionID), pick.Name)
	return nil
}

// prompt sends text as a prompt; one sent during a turn steers it. Before
// the session is ready it is queued and sent once it is.
func (h *host) prompt(text string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopping || h.exited {
		return errors.New("agent is stopping")
	}
	if !h.ready {
		h.pending = append(h.pending, text)
		return nil
	}
	h.send(text)
	return nil
}

// send records text and sends it as session/prompt. The turn ends when the
// last prompt in flight returns: then a question the agent left open is
// moot and is answered "cancelled". A prompt that fails while others run
// (a rejected steer) only leaves an error entry. Caller holds mu.
func (h *host) send(text string) {
	h.flush()
	h.emit(Entry{Kind: KindUser, Text: text}, true)
	h.inflight++
	h.apply(h.tr.PromptSent())
	c, sid := h.conn, h.sid
	go func() {
		r, err := c.Prompt(h.bg, sid, text)
		h.mu.Lock()
		defer h.mu.Unlock()
		h.inflight--
		select {
		case <-c.Done():
			return // the exit is reported by run
		default:
		}
		if h.stopping {
			return
		}
		if err != nil {
			h.emit(Entry{Kind: KindError, Text: "prompt: " + err.Error()}, true)
		}
		if r.Usage != nil {
			h.emit(Entry{Kind: KindUsage, Usage: r.Usage}, true)
		}
		if h.inflight == 0 {
			for len(h.questions) > 0 {
				h.dropQuestion(h.questions[0], acp.CancelledOutcome(), "cancelled")
			}
		}
		h.apply(h.tr.PromptDone(r.StopReason, err))
	}()
}

// cancel sends session/cancel and answers open questions "cancelled", as
// ACP asks of a client that cancels.
func (h *host) cancel() error {
	h.mu.Lock()
	c, sid := h.conn, h.sid
	h.mu.Unlock()
	if c == nil || sid == "" {
		return errors.New("session not ready")
	}
	err := c.Cancel(sid)
	h.mu.Lock()
	for len(h.questions) > 0 {
		h.resolve(h.questions[0], acp.CancelledOutcome(), "cancelled")
	}
	h.mu.Unlock()
	return err
}

func (h *host) requestStop() { h.once.Do(func() { close(h.stopC) }) }

// shutdown closes the session if the agent offers it, closes the client and
// waits for the child, killing it when it doesn't exit in time.
func (h *host) shutdown(cmd *exec.Cmd, exited <-chan struct{}) {
	h.mu.Lock()
	h.stopping = true
	for len(h.questions) > 0 {
		h.resolve(h.questions[0], acp.CancelledOutcome(), "cancelled")
	}
	h.flush()
	c, sid, caps := h.conn, h.sid, h.caps
	h.mu.Unlock()
	if c == nil {
		return
	}
	if sid != "" && caps.CanClose() {
		ctx, cancel := context.WithTimeout(h.bg, closeTimeout)
		if err := c.CloseSession(ctx, sid); err != nil {
			logx.For("acphost").Info("close session", "agent", h.cfg.Name, "err", err)
		}
		cancel()
	}
	_ = c.Close()
	select {
	case <-exited:
		return
	case <-time.After(exitTimeout):
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	select {
	case <-exited:
	case <-time.After(exitTimeout):
	}
}

// childExited reports the child's end: status error, an error entry and
// the error Run returns.
func (h *host) childExited(werr error) error {
	msg := "agent exited"
	if werr != nil {
		msg += ": " + werr.Error()
	}
	h.mu.Lock()
	h.exited = true
	for len(h.questions) > 0 {
		h.resolve(h.questions[0], acp.CancelledOutcome(), "cancelled")
	}
	h.flush()
	h.emit(Entry{Kind: KindError, Text: msg}, true)
	h.keepUnsent()
	h.apply(h.tr.Exited(werr))
	h.mu.Unlock()
	return errors.New("acp-host: " + msg)
}

// fail records a setup failure: an error entry and status error.
func (h *host) fail(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.emit(Entry{Kind: KindError, Text: err.Error()}, true)
	h.keepUnsent()
	h.apply(h.tr.Exited(err))
}

// keepUnsent saves the prompts of a host that ends before its session was
// ready (the first prompt and any queued since) in the prompt file, where
// the next host for the agent finds them, and says so in the transcript.
// Caller holds mu.
func (h *host) keepUnsent() {
	if h.ready {
		return
	}
	var all []string
	if h.cfg.Prompt != "" {
		all = append(all, h.cfg.Prompt)
	}
	all = append(all, h.pending...)
	h.pending = nil
	if len(all) == 0 {
		return
	}
	path := PromptPath(h.home, h.cfg.Name)
	text := fmt.Sprintf("%d prompt(s) not sent; kept in %s for the next start", len(all), path)
	if err := os.WriteFile(path, []byte(strings.Join(all, "\n\n")), 0o600); err != nil {
		text = fmt.Sprintf("%d prompt(s) not sent and not kept (%v)", len(all), err)
	}
	for _, p := range all {
		text += "\n  > " + clip(firstLine(p))
	}
	h.emit(Entry{Kind: KindError, Text: text}, true)
}

// input reads typed lines: a digit answers the open question, anything
// else is a prompt.
func (h *host) input(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		h.mu.Lock()
		open := len(h.questions) > 0
		h.mu.Unlock()
		var err error
		if n, nerr := strconv.Atoi(line); open && nerr == nil {
			err = h.answer(n, "")
		} else {
			err = h.prompt(line)
		}
		if err != nil {
			h.mu.Lock()
			fmt.Fprintln(h.out, "! "+err.Error())
			h.mu.Unlock()
		}
	}
}

// snapshot is the host's state for a control response.
func (h *host) snapshot() Response {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := Response{OK: true, Status: h.state.Status, SessionID: h.sid}
	if len(h.questions) > 0 {
		q := h.questions[0]
		r.Question = &Question{Title: q.title, Options: options(q.p)}
	}
	return r
}
