// Package mcp is a minimal stdio MCP server (JSON-RPC 2.0, one message per
// line) exposing fleet operations as tools for the admiral. Only protocol
// messages go to stdout; everything else goes to fleet.log.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/tools"
	"github.com/sturlabragason/fleet/internal/update"
	"github.com/sturlabragason/fleet/internal/wall"
)

// Instructions are the admiral rules, sent as MCP server instructions.
//
//go:embed instructions.md
var Instructions string

// ProtocolVersion is the latest MCP revision we speak; legacyVersions are
// all the ones initialize accepts. Fleet is tools-only, and nothing it sends
// differs between them (DESIGN §6.9), so only the answered version changes.
const ProtocolVersion = "2025-11-25"

var legacyVersions = []string{ProtocolVersion, "2025-06-18"}

// negotiate is the spec's version negotiation: the client's version when we
// support it, else our latest.
func negotiate(asked string) string {
	if slices.Contains(legacyVersions, asked) {
		return asked
	}
	return ProtocolVersion
}

// Server serves fleet tools.
type Server struct {
	F *fleet.Fleet
	W *wall.Wall
	// Fill reports the admiral's context use (tests replace it).
	Fill func() (used, max int, ok bool)
	// Reset asks tmux to replace the admiral after a handoff (tests stub it).
	Reset func() error
	// SwapProfile schedules `fleet admiral profile NAME` (tests stub it).
	SwapProfile func(name string) error
	// RunCLI runs a fleet subcommand for the CLI-backed tools (ADR-86):
	// output, exit code (tests stub it; default: the fleet binary).
	RunCLI func(ctx context.Context, dir string, args ...string) (string, int, error)
	// RulesInPrompt: the client got the admiral rules as a system prompt
	// file, so initialize sends a pointer instead (ADR-86; Claude Code cuts
	// MCP server instructions at about 4 KB).
	RulesInPrompt bool
	// Agent, when set, is agent mode (DESIGN §6.6): only the peer tools,
	// acting as this agent.
	Agent string
	// Admiral is the admiral this server speaks for: "" is main, and the
	// only one before several admirals existed (DESIGN §6.12).
	Admiral string
	// External: Admiral is an MCP client outside tmux with no pane
	// (ADR-159): short instructions, a shorter fleet_wait, no reset, no
	// profile swap, no context warning.
	External bool

	// Hot reload (DESIGN §6.9). Zero values get real defaults in Serve.
	// ExePath is the executable to watch (default F.Exe; "" disables reload).
	ExePath string
	// Probe checks a new binary before exec (default `<path> mcp --probe`).
	Probe func(path string) error
	// Exec replaces this process (default sysx.Exec).
	Exec func(path string, argv, env []string) error
	Now  func() time.Time
	// Reloaded marks a process started by a reload: Serve first sends
	// tools/list_changed and the first tool result gets a note.
	Reloaded bool

	exeInfo  os.FileInfo // executable identity at start
	failInfo os.FileInfo // last binary whose probe or exec failed
	failAt   time.Time
	selfExe  string        // running executable (default /proc/self/exe; tests set it)
	pending  *resumedInput // input left over from the previous reload
	noted    bool          // reload note already appended
	// served is the hash of the instructions the client last got, inherited
	// across a reload (ADR-102).
	served string
	// client is who connected (initialize's clientInfo and capabilities),
	// inherited across a reload; it decides the confirmation gate (ADR-213).
	client clientInfo
	// suffixSeen holds the unread reports whose headline reportsSuffix
	// already named (ADR-195); guarded by mu.
	suffixSeen map[string]bool
	// had is the hash of the instructions text the client holds, the base
	// of fleet_instructions' diff (ADR-195).
	had string
	mu  sync.Mutex

	// The concurrent serve loop (cancel.go, ADR-214). out writes protocol
	// messages, guarded by omu, which also guards werr (the first failed
	// write). calls (requests in flight by id), flight (lines in the lane,
	// with or without an id) and replies (waiters for responses to fleet's
	// own requests, elicit.go) are guarded by wmu; idle is signalled when
	// flight drops or a waiter registers. tail (the lane's last call) belongs
	// to the reader; cur, the running call, to the lane: only the call whose
	// turn it is touches it.
	base    context.Context // parent of every call's context; stop ends them all
	stop    context.CancelCauseFunc
	out     *json.Encoder
	omu     sync.Mutex
	werr    error
	wmu     sync.Mutex
	idle    sync.Cond
	calls   map[string]*call
	flight  int
	replies map[string]chan []byte
	tail    chan struct{}
	cur     *call
	// modern (a modern request arrived) and subs (the open
	// subscriptions/listen requests, modern.go) belong to the reader and
	// cross a reload in envListen.
	modern     bool
	subs       []listenSub
	dialogWait time.Duration // replaces dialogWait in tests
	// spent holds the nonces of MRTR answers already used, with their
	// expiry (mrtr.go); guarded by mu.
	spent map[string]int64
}

// New returns a server.
func New(f *fleet.Fleet, w *wall.Wall) *Server { return NewAdmiral(f, w, "") }

// Unknown is the Admiral of a server that could not tell which admiral it
// serves: reads work, every change is refused (DESIGN §6.12).
const Unknown = "?"

// NewAdmiral is New for one admiral by name ("" or main = the first one,
// Unknown = identity not known).
func NewAdmiral(f *fleet.Fleet, w *wall.Wall, admiral string) *Server {
	if admiral == board.Main {
		admiral = ""
	}
	s := &Server{F: f, W: w, Admiral: admiral}
	s.Fill = func() (int, int, bool) {
		return supervisor.CurrentFillOf(f.Home, f.T, fleet.Session+":main", s.Me())
	}
	s.Reset = func() error {
		// tmux runs the reset, so it survives this MCP server being killed with the admiral.
		_, err := f.T.Run("run-shell", "-b", resetShell(tools.Quote(f.Exe), admiral))
		return err
	}
	s.SwapProfile = func(name string) error {
		// Like Reset: tmux runs the swap, which replaces this server's pane.
		_, err := f.T.Run("run-shell", "-b", "sleep 2; "+tools.Quote(f.Exe)+" admiral profile"+nameFlag(admiral)+" "+tools.Quote(name)+" >/dev/null 2>&1")
		return err
	}
	return s
}

// contextWarning is appended to tool results once the admiral's context
// is at or above the handoff threshold ("" otherwise).
func (s *Server) contextWarning() string {
	if s.Fill == nil || s.External {
		return ""
	}
	used, max, ok := s.Fill()
	if !ok {
		return ""
	}
	if !supervisor.HandoffDue(used, max) {
		return ""
	}
	if s.F != nil {
		s.F.Intervene(mail.BoxOf(s.Admiral), board.KindContextWarning, fmt.Sprintf("%d%%", supervisor.Pct(used, max)))
	}
	return fmt.Sprintf("\n\n[admiral context %d%%, %dk tokens: save anything pending with fleet_note, then call fleet_handoff]", supervisor.Pct(used, max), used/1000)
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// errUnknownTool marks a tools/call for a name this server does not serve;
// handle answers it with a -32602 protocol error, not an isError result.
var errUnknownTool = errors.New("unknown tool")

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve reads requests from r and writes responses to w until EOF. It reads
// every line as it arrives and serves each request on its own goroutine, in
// arrival order (cancel.go, ADR-214). Before each line it reloads the binary
// in place if it changed on disk (DESIGN §6.9).
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	log := logx.For("mcp")
	log.Info("start", "reloaded", s.Reloaded)
	s.initReload()
	if ri, ok := r.(*resumedInput); ok {
		s.pending = ri
	}
	s.takeServedHash()
	s.takeReloadedClient()
	s.takeListen()
	br := bufio.NewReader(r)
	s.out = json.NewEncoder(w)
	if s.Reloaded {
		if err := s.announceReload(); err != nil {
			return err
		}
	}
	s.idle.L = &s.wmu
	s.base, s.stop = context.WithCancelCause(context.Background())
	defer s.finish()
	for {
		raw, rerr := readLine(br)
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			log.Info("stdin closed", "err", rerr)
			return rerr
		}
		if err := s.writeErr(); err != nil { // before a reload, which would exec onto it
			log.Info("stdout closed", "err", err)
			return err
		}
		if line := bytes.TrimRight(raw, "\r\n"); len(line) > 0 && !s.deliver(line) {
			s.arrived(line)
			if s.reloadNow(raw, br, w) {
				return nil // a stubbed Exec "replaced" the process
			}
			s.dispatch(line)
		}
		if errors.Is(rerr, io.EOF) {
			log.Info("stdin closed")
			s.finish()
			return s.writeErr() // a call answered while finishing may have failed to write
		}
	}
}

func (s *Server) handle(req request) (res any, rerr *rpcError) {
	log := logx.For("mcp")
	defer func() {
		if r := recover(); r != nil {
			log.Error("panic", "method", req.Method, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			rerr = &rpcError{Code: -32603, Message: fmt.Sprint(r)}
		}
	}()
	if s.cur != nil && s.cur.peer != nil {
		if rerr := modernCheck(req.Method, s.cur.peer.Protocol); rerr != nil {
			return nil, rerr
		}
	}
	switch req.Method {
	case "initialize":
		s.takeClient(req.Params)
		return map[string]any{
			"protocolVersion": s.client.Protocol,
			"capabilities":    serverCaps(),
			"serverInfo":      serverInfo(),
			"instructions":    s.serveInstructions(),
		}, nil
	case "server/discover":
		return s.discover(), nil
	case "notifications/initialized":
		return nil, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		if s.Agent != "" {
			return map[string]any{"tools": s.agentTools()}, nil
		}
		return map[string]any{"tools": s.listTools()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Meta      struct {
				ProgressToken json.RawMessage `json:"progressToken"`
			} `json:"_meta"`
			InputResponses map[string]json.RawMessage `json:"inputResponses"`
			RequestState   string                     `json:"requestState"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid params"}
		}
		if s.Agent == "" && s.F != nil {
			extOnCall(s)
		}
		if s.cur != nil {
			s.cur.progress = s.progressFor(p.Meta.ProgressToken)
			if s.cur.peer != nil {
				s.cur.mrtr = &mrtrIn{tool: p.Name, args: p.Arguments, responses: p.InputResponses, state: p.RequestState}
			}
		}
		text, err := s.callTool(p.Name, p.Arguments)
		log.Info("call", "tool", p.Name, "agent", s.Agent, "err", err)
		if s.cur.cancelled() {
			// No reply goes out, so no one-time note (reports, reload, upgrade) may be spent on it.
			return nil, nil
		}
		if errors.Is(err, errInputRequired) && s.cur.mrtr != nil && s.cur.mrtr.result != nil {
			return s.cur.mrtr.result, nil // no suffixes: one-time notes wait for the real result
		}
		if errors.Is(err, errUnknownTool) {
			// No suffixes: pending reload and instructions notes wait for the next real result.
			return nil, &rpcError{Code: -32602, Message: "Unknown tool: " + p.Name}
		}
		isErr := err != nil
		if isErr {
			text = err.Error()
		} else {
			text += extAfter(s, p.Name, p.Arguments, text)
		}
		switch {
		case s.Agent != "":
			text += s.unreadSuffix()
		case p.Name != "fleet_handoff" && p.Name != "fleet_admiral_profile":
			text += s.contextWarning() + s.toolsWarning() + s.reportsSuffix() + extSuffix(s)
		}
		if s.Reloaded && !s.noted {
			s.noted = true
			text += reloadedNote + s.staleNote()
		}
		if s.Agent == "" && s.F != nil {
			text += update.Announce(s.F.Home, fleet.Version, s.Admiral, time.Now()) // once per upgrade (ADR-114)
		}
		text += s.instructionsNote(p.Name)
		return toolResult(text, isErr), nil
	}
	return nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method}
}

// callTool runs one tool under s.mu; the unlock is deferred so a recovered
// panic (handle) does not leave it locked.
func (s *Server) callTool(name string, raw json.RawMessage) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case name == "fleet_instructions":
		return s.currentInstructions(raw), nil
	case s.Agent != "":
		return s.agentCall(name, raw)
	}
	return s.call(name, raw)
}

// progressFor sends progress notifications for one request's token; nil
// when the client sent none.
func (s *Server) progressFor(tok json.RawMessage) func(int, string) {
	if len(tok) == 0 || string(tok) == "null" {
		return nil
	}
	return func(n int, msg string) {
		if err := s.write(map[string]any{"jsonrpc": "2.0", "method": "notifications/progress",
			"params": map[string]any{"progressToken": tok, "progress": n, "message": msg}}); err != nil {
			logx.For("mcp").Debug("progress not sent", "err", err)
		}
	}
}

func toolResult(text string, isErr bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isErr}
}
