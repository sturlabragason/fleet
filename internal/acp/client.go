package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sturlabragason/fleet/internal/logx"
	"io"
	"os"
	"os/exec"
	"time"
)

// Handler receives what the agent sends on its own.
type Handler interface {
	// SessionUpdate is called in arrival order, never concurrently.
	SessionUpdate(n SessionNotification)
	// RequestPermission runs on its own goroutine and may block until the
	// question is answered; ctx is cancelled when the client closes.
	RequestPermission(ctx context.Context, p RequestPermissionParams) (RequestPermissionResult, error)
	// CopilotEvent is called in arrival order with SessionUpdate.
	CopilotEvent(e CopilotEvent)
}

// Client is an ACP client over one connection. Handler callbacks for
// notifications run on the client's dispatch goroutine: they must not wait
// for a call on the same client, or that call never completes.
type Client struct {
	c *conn
	h Handler
}

// NewClient starts reading agent frames from r; requests go to w.
func NewClient(r io.Reader, w io.Writer, h Handler) *Client {
	return newClient(r, w, h, nil)
}

func newClient(r io.Reader, w io.Writer, h Handler, extra chan struct{}) *Client {
	cl := &Client{h: h}
	cl.c = newConn(r, w, cl.notified, cl.requested, extra)
	return cl
}

// exitGrace is how long Start waits, after the child exits, for its stdout
// to close before it closes the pipe itself (a grandchild may hold it).
var exitGrace = 2 * time.Second

// Start runs argv in dir with env (nil: inherit) as an ACP server and
// connects to its stdio; its stderr goes to stderr (nil: discarded). ctx
// kills the child. The returned Cmd is already waited for: use Done and Err,
// never cmd.Wait.
func Start(ctx context.Context, argv []string, dir string, env []string, stderr io.Writer, h Handler) (*Client, *exec.Cmd, error) {
	if len(argv) == 0 {
		return nil, nil, fmt.Errorf("acp: empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stderr = stderr
	grace := exitGrace
	cmd.WaitDelay = grace // a grandchild holding stderr must not block Wait
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stdout = pw
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return nil, nil, err
	}
	pw.Close()
	exited := make(chan struct{})
	cl := newClient(pr, stdin, h, exited)
	go func() {
		werr := cmd.Wait()
		if errors.Is(werr, exec.ErrWaitDelay) {
			werr = nil // exited 0; only its stderr stayed open
		}
		cl.c.setExit(werr)
		close(exited)
		if werr != nil {
			logx.For("acp").Info("acp: agent exited", "cmd", argv[0], "err", werr)
		}
		select {
		case <-cl.c.done:
		case <-time.After(grace):
			cl.c.forceClose(pr)
		}
	}()
	return cl, cmd, nil
}

func (cl *Client) notified(method string, params json.RawMessage) {
	if cl.h == nil {
		return
	}
	switch method {
	case MethodSessionUpdate:
		var n SessionNotification
		if err := json.Unmarshal(params, &n); err != nil {
			logx.For("acp").Warn("acp: bad session/update skipped", "err", err)
			return
		}
		cl.h.SessionUpdate(n)
	case MethodCopilotEvent:
		var e CopilotEvent
		if err := json.Unmarshal(params, &e); err != nil {
			logx.For("acp").Warn("acp: bad copilot event skipped", "err", err)
			return
		}
		cl.h.CopilotEvent(e)
	default:
		logx.For("acp").Debug("acp: notification ignored", "method", method)
	}
}

func (cl *Client) requested(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method != MethodRequestPermission || cl.h == nil {
		return nil, &RPCError{Code: CodeMethodNotFound, Message: "method not found: " + method}
	}
	var p RequestPermissionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: CodeInvalidParams, Message: err.Error()}
	}
	return cl.h.RequestPermission(ctx, p)
}

// Initialize does the handshake. Fleet offers no file system and no
// terminal; copilotEvents opts in to Copilot's session events.
func (cl *Client) Initialize(ctx context.Context, copilotEvents bool) (InitializeResult, error) {
	p := InitializeParams{
		ProtocolVersion: ProtocolVersion,
		ClientInfo:      &Implementation{Name: "fleet"},
	}
	if copilotEvents {
		p.ClientCapabilities.Meta = map[string]any{
			"github.com/copilot": map[string]any{"events": CopilotEvents},
		}
	}
	var res InitializeResult
	err := cl.c.call(ctx, MethodInitialize, p, &res)
	return res, err
}

// NewSession starts a session in cwd and returns its id.
func (cl *Client) NewSession(ctx context.Context, cwd string, mcp []MCPServer) (string, error) {
	var res NewSessionResult
	if err := cl.c.call(ctx, MethodSessionNew, NewSessionParams{Cwd: cwd, MCPServers: nonNil(mcp)}, &res); err != nil {
		return "", err
	}
	if res.SessionID == "" {
		return "", fmt.Errorf("acp: session/new returned no sessionId")
	}
	return res.SessionID, nil
}

// LoadSession reopens a session; the agent replays its history as updates.
func (cl *Client) LoadSession(ctx context.Context, id, cwd string, mcp []MCPServer) error {
	return cl.c.call(ctx, MethodSessionLoad, SessionRefParams{SessionID: id, Cwd: cwd, MCPServers: nonNil(mcp)}, nil)
}

// ResumeSession reopens a session without replaying its history.
func (cl *Client) ResumeSession(ctx context.Context, id, cwd string, mcp []MCPServer) error {
	return cl.c.call(ctx, MethodSessionResume, SessionRefParams{SessionID: id, Cwd: cwd, MCPServers: nonNil(mcp)}, nil)
}

// Prompt sends text as the user's turn and blocks until the turn ends.
func (cl *Client) Prompt(ctx context.Context, id, text string) (StopReason, error) {
	var res PromptResult
	if err := cl.c.call(ctx, MethodSessionPrompt, PromptParams{SessionID: id, Prompt: []ContentBlock{TextBlock(text)}}, &res); err != nil {
		return "", err
	}
	return res.StopReason, nil
}

// Cancel asks the agent to stop the running turn (a notification); the
// pending Prompt then returns StopCancelled.
func (cl *Client) Cancel(id string) error {
	return cl.c.notify(MethodSessionCancel, SessionIDParams{SessionID: id})
}

// CloseSession ends a session (only if the agent offers session/close).
func (cl *Client) CloseSession(ctx context.Context, id string) error {
	return cl.c.call(ctx, MethodSessionClose, SessionIDParams{SessionID: id}, nil)
}

// Done is closed when the reader ends (EOF, or for Start the child's exit)
// and every frame read before that was dispatched.
func (cl *Client) Done() <-chan struct{} { return cl.c.done }

// Err says why the client ended: the child's exit error, a read error, or
// nil on a clean EOF. Nil while Done is open.
func (cl *Client) Err() error { return cl.c.err() }

// Close closes the writer (the agent's stdin), fails pending calls and
// cancels running RequestPermission handlers.
func (cl *Client) Close() error { return cl.c.close() }

func nonNil(m []MCPServer) []MCPServer {
	if m == nil {
		return []MCPServer{}
	}
	return m
}
