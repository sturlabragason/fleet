package kagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sturlabragason/fleet/internal/acp"
	"github.com/sturlabragason/fleet/internal/logx"
)

// Bridge is the ACP side of `fleet kagent acp`: one ACP client (fleet's
// acp-host) on stdio, each ACP session one kagent conversation.
type Bridge struct {
	Gateway Gateway
	// Connect returns the gateway's base URL, once, before the first session
	// (kubectl port-forward). Nil when Gateway.URL is set.
	Connect func(ctx context.Context) (string, error)
	// StateDir keeps one file per session: its kagent context id, so a woken
	// agent continues the same conversation.
	StateDir string
	// Cluster names the kube context for the first prompt's preamble.
	Cluster string
	// Repo returns the origin URL and branch of a dir, for the preamble.
	Repo func(ctx context.Context, dir string) (url, branch string)
	// Report files a fleet report for the agent when its reply ends with a
	// `fleet_report <state>: <headline>` line. Nil: no reports.
	Report func(ctx context.Context, state, headline string) error

	out     io.Writer
	wmu     sync.Mutex
	mu      sync.Mutex
	conn    sync.Once
	connErr error
	sess    map[string]*session
	wg      sync.WaitGroup
}

// state is a session's file in StateDir.
type state struct {
	ID      string `json:"id"`
	Context string `json:"context,omitempty"` // kagent Session id (A2A contextId)
	Task    string `json:"task,omitempty"`    // a task waiting for input
	Cwd     string `json:"cwd"`
}

type session struct {
	state
	cancel context.CancelFunc // the running turn's, nil when idle
	taskID string             // the running turn's task
}

// Serve answers ACP requests from in on out until in ends.
func (b *Bridge) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	b.out, b.sess = out, map[string]*session{}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 64<<20)
	for sc.Scan() {
		var m acp.Message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil || m.Method == "" {
			continue // responses: the bridge sends no requests
		}
		b.handle(ctx, m)
	}
	b.mu.Lock()
	for _, s := range b.sess {
		if s.cancel != nil {
			s.cancel()
		}
	}
	b.mu.Unlock()
	b.wg.Wait()
	if err := sc.Err(); err != nil {
		return fmt.Errorf("kagent acp: read: %w", err)
	}
	return nil
}

type handler func(b *Bridge, ctx context.Context, m acp.Message) (any, error)

var handlers = map[string]handler{
	acp.MethodInitialize:    (*Bridge).initialize,
	acp.MethodSessionNew:    (*Bridge).newSession,
	acp.MethodSessionResume: (*Bridge).resume,
	acp.MethodSessionClose:  (*Bridge).closeSession,
}

func (b *Bridge) handle(ctx context.Context, m acp.Message) {
	switch m.Method {
	case acp.MethodSessionCancel:
		b.cancelTurn(ctx, m)
		return
	case acp.MethodSessionPrompt:
		b.prompt(ctx, m)
		return
	}
	if len(m.ID) == 0 {
		return
	}
	h := handlers[m.Method]
	if h == nil {
		b.fail(m.ID, &acp.RPCError{Code: acp.CodeMethodNotFound, Message: "method not found: " + m.Method})
		return
	}
	res, err := h(b, ctx, m)
	if err != nil {
		b.fail(m.ID, err)
		return
	}
	b.write(acp.Message{ID: m.ID, Result: raw(res)})
}

func (b *Bridge) initialize(context.Context, acp.Message) (any, error) {
	return acp.InitializeResult{
		ProtocolVersion: acp.ProtocolVersion,
		AgentCapabilities: acp.AgentCapabilities{SessionCapabilities: acp.SessionCapabilities{
			Resume: json.RawMessage(`{}`), Close: json.RawMessage(`{}`)}},
		AgentInfo: &acp.Implementation{Name: "fleet-kagent", Version: "1"},
	}, nil
}

// connect resolves the gateway URL once; a failure fails every session.
func (b *Bridge) connect(ctx context.Context) error {
	b.conn.Do(func() {
		if b.Connect == nil {
			return
		}
		url, err := b.Connect(ctx)
		b.mu.Lock()
		b.Gateway.URL, b.connErr = url, err
		b.mu.Unlock()
	})
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.connErr
}

func (b *Bridge) newSession(ctx context.Context, m acp.Message) (any, error) {
	var p acp.NewSessionParams
	_ = json.Unmarshal(m.Params, &p)
	if err := b.connect(ctx); err != nil {
		return nil, err
	}
	if len(p.MCPServers) > 0 {
		logx.For("kagent").Info("local MCP servers are not passed to a cluster agent", "servers", len(p.MCPServers))
	}
	s := &session{state: state{ID: "kagent-" + NewID()[:16], Cwd: p.Cwd}}
	if err := b.save(s.state); err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.sess[s.ID] = s
	b.mu.Unlock()
	return map[string]string{"sessionId": s.ID}, nil
}

func (b *Bridge) resume(ctx context.Context, m acp.Message) (any, error) {
	var p acp.SessionRefParams
	_ = json.Unmarshal(m.Params, &p)
	data, err := os.ReadFile(b.path(p.SessionID))
	if err != nil {
		return nil, &acp.RPCError{Code: acp.CodeInvalidParams, Message: "unknown kagent session " + p.SessionID}
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("kagent session %s: %w", p.SessionID, err)
	}
	if err := b.connect(ctx); err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.sess[st.ID] = &session{state: st}
	b.mu.Unlock()
	return struct{}{}, nil
}

// closeSession forgets the session here. The kagent Session stays: kagent
// suspends an idle one, and the user deletes it with kagent's own tools.
func (b *Bridge) closeSession(_ context.Context, m acp.Message) (any, error) {
	var p acp.SessionIDParams
	_ = json.Unmarshal(m.Params, &p)
	b.mu.Lock()
	delete(b.sess, p.SessionID)
	b.mu.Unlock()
	return struct{}{}, nil
}

func (b *Bridge) cancelTurn(ctx context.Context, m acp.Message) {
	var p acp.SessionIDParams
	_ = json.Unmarshal(m.Params, &p)
	b.mu.Lock()
	s := b.sess[p.SessionID]
	var cancel context.CancelFunc
	task := ""
	if s != nil {
		cancel, task = s.cancel, s.taskID
	}
	gw := b.Gateway
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if task == "" {
		return
	}
	b.wg.Go(func() {
		cctx, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer stop()
		if err := gw.Cancel(cctx, task); err != nil {
			logx.For("kagent").Warn("cancel task", "task", task, "err", err)
		}
	})
}

func (b *Bridge) path(id string) string {
	return filepath.Join(b.StateDir, filepath.Base(id)+".json")
}

func (b *Bridge) save(st state) error {
	if err := os.MkdirAll(b.StateDir, 0o700); err != nil {
		return fmt.Errorf("kagent state: %w", err)
	}
	data, _ := json.Marshal(st)
	if err := os.WriteFile(b.path(st.ID), data, 0o600); err != nil {
		return fmt.Errorf("kagent state: %w", err)
	}
	return nil
}

func raw(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func (b *Bridge) write(m acp.Message) {
	m.JSONRPC = "2.0"
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	b.wmu.Lock()
	defer b.wmu.Unlock()
	_, _ = b.out.Write(append(data, '\n'))
}

func (b *Bridge) fail(id json.RawMessage, err error) {
	var re *acp.RPCError
	if !errors.As(err, &re) {
		re = &acp.RPCError{Code: acp.CodeInternal, Message: err.Error()}
	}
	b.write(acp.Message{ID: id, Error: re})
}

func (b *Bridge) update(id string, u acp.SessionUpdate) {
	b.write(acp.Message{Method: acp.MethodSessionUpdate, Params: raw(acp.SessionNotification{SessionID: id, Update: u})})
}

func (b *Bridge) say(id, text string) {
	if text == "" {
		return
	}
	b.update(id, acp.SessionUpdate{Kind: acp.UpdateAgentMessage, Content: raw(acp.TextBlock(text))})
}
