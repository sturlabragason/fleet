// Package mcp is a minimal stdio MCP server (JSON-RPC 2.0, one message per
// line) exposing fleet operations as tools for the admiral. Only protocol
// messages go to stdout; everything else goes to fleet.log.
package mcp

import (
	"bufio"
	"bytes"
	"cmp"
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
// differs between them (DESIGN §6.2a), so only the answered version changes.
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

	// out writes protocol messages; progress, while a tools/call that
	// carries a progressToken runs, sends it a notifications/progress
	// (fleet_wait, ADR-72). Both are used only on the serving goroutine.
	out      *json.Encoder
	progress func(n int, msg string)

	// Read-ahead for cancellable calls (cancel.go): in is Serve's input;
	// ahead holds lines a reader goroutine read while a call ran, aheadDone
	// is closed when that reader has pushed its last line. watching,
	// reading, queued and cancelled are guarded by wmu; callID and drop
	// belong to the serving goroutine.
	in        *bufio.Reader
	ahead     chan lineRead
	aheadDone chan struct{}
	wmu       sync.Mutex
	watching  *watch
	reading   bool
	queued    int             // lines the reader checked, not yet taken by next
	cancelled map[string]bool // ids of cancel lines the reader saw for no running call
	callID    json.RawMessage // id of the tools/call being served
	drop      bool            // the client cancelled it: send no response
	// replies routes responses to fleet's own requests (elicit.go) by id;
	// guarded by wmu. dialogWait replaces dialogWait in tests.
	replies    map[string]chan []byte
	dialogWait time.Duration
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
	s.takeServedHash()
	s.takeReloadedClient()
	if s.Reloaded {
		if _, err := io.WriteString(w, listChanged); err != nil {
			return err
		}
	}
	br := bufio.NewReader(r)
	s.in = br
	enc := json.NewEncoder(w)
	for {
		raw, rerr := s.next()
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			log.Info("stdin closed", "err", rerr)
			return rerr
		}
		line := bytes.TrimRight(raw, "\r\n")
		if len(line) > 0 {
			// A reload hands br over, so only when no reader holds it.
			if s.aheadIdle() && s.maybeReload(raw, br, w) {
				return nil // a stubbed Exec "replaced" the process
			}
			if err := s.serveLine(line, enc); err != nil {
				return err
			}
		}
		if errors.Is(rerr, io.EOF) {
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
	if req.Method == "" {
		if _, ok := responseID(line); ok { // late or stray: a response is never answered
			logx.For("mcp").Info("response nobody waits for", "id", string(req.ID))
			return nil
		}
	}
	s.out = enc
	s.drop = false
	res, rerr := s.handle(req)
	if len(req.ID) == 0 { // notification
		return nil
	}
	if s.drop { // the spec: no response to a request the client cancelled
		logx.For("mcp").Info("cancelled by the client", "id", string(req.ID))
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
		s.takeClient(req.Params)
		s.served = instructionsHash(s.rulesText())
		s.had = s.served
		s.keepInstructions(s.rulesText())
		return map[string]any{
			"protocolVersion": s.client.Protocol,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
			"serverInfo":      map[string]any{"name": "fleet", "title": "Fleet", "version": cmp.Or(fleet.Version, "dev")},
			"instructions":    s.instructions(),
		}, nil
	case "notifications/initialized":
		return nil, nil
	case "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(req.Params, &p) == nil {
			s.forgetCancel(p.RequestID)
		}
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
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{-32602, "invalid params"}
		}
		if s.Agent == "" && s.F != nil {
			extOnCall(s)
		}
		s.mu.Lock()
		s.progress = s.progressFor(p.Meta.ProgressToken)
		s.callID = req.ID
		var text string
		var err error
		switch {
		case p.Name == "fleet_instructions":
			text = s.currentInstructions(p.Arguments)
		case s.Agent != "":
			text, err = s.agentCall(p.Name, p.Arguments)
		default:
			text, err = s.call(p.Name, p.Arguments)
		}
		s.progress = nil
		s.mu.Unlock()
		log.Info("call", "tool", p.Name, "agent", s.Agent, "err", err)
		if s.drop {
			// No reply goes out, so no one-time note (reports, reload, upgrade) may be spent on it.
			return nil, nil
		}
		if errors.Is(err, errUnknownTool) {
			// No suffixes: pending reload and instructions notes wait for the next real result.
			return nil, &rpcError{-32602, "Unknown tool: " + p.Name}
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
		if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/progress",
			"params": map[string]any{"progressToken": tok, "progress": n, "message": msg}}); err != nil {
			logx.For("mcp").Debug("progress not sent", "err", err)
		}
	}
}

func toolResult(text string, isErr bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isErr}
}
