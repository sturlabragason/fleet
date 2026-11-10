// Package mcp is a minimal stdio MCP server (JSON-RPC 2.0, one message per
// line) exposing fleet operations as tools for the admiral. Only protocol
// messages go to stdout; everything else goes to fleet.log.
package mcp

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/tools"
	"github.com/sturlabragason/fleet/internal/wall"
)

// Instructions are the admiral rules, sent as MCP server instructions.
//
//go:embed instructions.md
var Instructions string

// ProtocolVersion is the MCP revision we speak.
const ProtocolVersion = "2025-06-18"

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
	RunCLI func(dir string, args ...string) (string, int, error)
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

	// Hot reload (DESIGN §6.9). Zero values get real defaults in Serve.
	// ExePath is the executable to watch (default F.Exe; "" disables reload).
	ExePath string
	// Probe checks a new binary before exec (default `<path> mcp --probe`).
	Probe func(path string) error
	// Exec replaces this process (default syscall.Exec).
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
	mu       sync.Mutex

	// out writes protocol messages; progress, while a tools/call that
	// carries a progressToken runs, sends it a notifications/progress
	// (fleet_wait, ADR-72). Both are used only on the serving goroutine.
	out      *json.Encoder
	progress func(n int, msg string)
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
	if s.Fill == nil {
		return ""
	}
	used, max, ok := s.Fill()
	if !ok {
		return ""
	}
	pct := supervisor.Pct(used, max)
	if pct < supervisor.Threshold() {
		return ""
	}
	return fmt.Sprintf("\n\n[admiral context %d%%: save anything pending with fleet_note, then call fleet_handoff]", pct)
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve reads requests from r and writes responses to w until EOF. Before each
// request it reloads the binary in place if it changed on disk (DESIGN §6.9).
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	log := logx.For("mcp")
	log.Info("start", "reloaded", s.Reloaded)
	s.initReload()
	if ri, ok := r.(*resumedInput); ok {
		s.pending = ri
	}
	if s.Reloaded {
		if _, err := io.WriteString(w, listChanged); err != nil {
			return err
		}
	}
	br := bufio.NewReader(r)
	enc := json.NewEncoder(w)
	for {
		raw, rerr := readLine(br)
		if rerr != nil && rerr != io.EOF {
			log.Info("stdin closed", "err", rerr)
			return rerr
		}
		line := bytes.TrimRight(raw, "\r\n")
		if len(line) > 0 {
			if s.maybeReload(raw, br, w) {
				return nil // a stubbed Exec "replaced" the process
			}
			if err := s.serveLine(line, enc); err != nil {
				return err
			}
		}
		if rerr == io.EOF {
			log.Info("stdin closed")
			return nil
		}
	}
}

func (s *Server) serveLine(line []byte, enc *json.Encoder) error {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		logx.For("mcp").Warn("bad json", "err", err)
		return enc.Encode(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
	}
	s.out = enc
	res, rerr := s.handle(req)
	if len(req.ID) == 0 { // notification
		return nil
	}
	resp := response{JSONRPC: "2.0", ID: req.ID, Result: res, Error: rerr}
	if rerr == nil && res == nil {
		resp.Result = struct{}{}
	}
	return enc.Encode(resp)
}

func (s *Server) handle(req request) (res any, rerr *rpcError) {
	log := logx.For("mcp")
	defer func() {
		if r := recover(); r != nil {
			log.Error("panic", "method", req.Method, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			rerr = &rpcError{-32603, fmt.Sprint(r)}
		}
	}()
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
			"serverInfo":      map[string]any{"name": "fleet", "version": "0.1.0"},
			"instructions":    s.instructions(),
		}, nil
	case "notifications/initialized", "notifications/cancelled":
		return nil, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		if s.Agent != "" {
			return map[string]any{"tools": s.agentTools()}, nil
		}
		return map[string]any{"tools": toolDefs}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Meta      struct {
				ProgressToken json.RawMessage `json:"progressToken"`
			} `json:"_meta"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{-32602, "invalid params"}
		}
		s.mu.Lock()
		s.progress = s.progressFor(p.Meta.ProgressToken)
		var text string
		var err error
		if s.Agent != "" {
			text, err = s.agentCall(p.Name, p.Arguments)
		} else {
			text, err = s.call(p.Name, p.Arguments)
		}
		s.progress = nil
		s.mu.Unlock()
		log.Info("call", "tool", p.Name, "agent", s.Agent, "err", err)
		isErr := err != nil
		if isErr {
			text = err.Error()
		}
		switch {
		case s.Agent != "":
			text += s.unreadSuffix()
		case p.Name != "fleet_handoff" && p.Name != "fleet_admiral_profile":
			text += s.contextWarning() + s.reportsSuffix()
		}
		if s.Reloaded && !s.noted {
			s.noted = true
			text += reloadedNote
		}
		return toolResult(text, isErr), nil
	}
	return nil, &rpcError{-32601, "method not found: " + req.Method}
}

// progressFor sends progress notifications for one request's token; nil
// when the client sent none.
func (s *Server) progressFor(tok json.RawMessage) func(int, string) {
	if len(tok) == 0 || string(tok) == "null" || s.out == nil {
		return nil
	}
	enc := s.out
	return func(n int, msg string) {
		enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/progress",
			"params": map[string]any{"progressToken": tok, "progress": n, "message": msg}})
	}
}

func toolResult(text string, isErr bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isErr}
}
