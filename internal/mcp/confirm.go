package mcp

// The user-confirmation gate (ADR-213). Every tool that needs the user's yes
// asks confirmed() and refuses with refuse(), so the rule lives here once.

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
)

// envClient carries the client's initialize identity across a hot reload
// (DESIGN §6.9): the new process never sees initialize again.
const envClient = "FLEET_MCP_CLIENT"

// clientInfo is what the client said about itself at initialize.
type clientInfo struct {
	Name         string          `json:"name,omitempty"`
	Version      string          `json:"version,omitempty"`
	Capabilities json.RawMessage `json:"capabilities,omitempty"`
	// Protocol is the negotiated MCP version.
	Protocol string `json:"protocol,omitempty"`
}

// nativeGated are the tools that always need the user's yes. For a client
// whose own permission prompt is a human gate (nativePrompt) tools/list marks
// them so the client asks the user on every call. Tools whose gate depends on
// an argument (send, team, worktrees, branches, admiral_add) are not here:
// the mark would prompt for their plain calls too.
var nativeGated = map[string]bool{"fleet_answer": true, "fleet_close": true, "fleet_access_decide": true}

// claudeCodeMin is the first Claude Code release whose changelog shows
// `_meta["anthropic/requiresUserInteraction"]` working as a gate: 2.1.246
// stopped its prompt offering "don't ask again" (code.claude.com/docs/en/mcp,
// "Require approval for a specific tool").
const claudeCodeMin = "2.1.246"

// takeClient reads initialize's params.
func (s *Server) takeClient(params json.RawMessage) {
	var p struct {
		ProtocolVersion string          `json:"protocolVersion"`
		ClientInfo      clientInfo      `json:"clientInfo"`
		Capabilities    json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		logx.For("mcp").Debug("initialize params", "err", err)
	}
	s.client = clientInfo{Name: p.ClientInfo.Name, Version: p.ClientInfo.Version, Capabilities: p.Capabilities, Protocol: negotiate(p.ProtocolVersion)}
	logx.For("mcp").Info("client", "name", s.client.Name, "version", s.client.Version, "protocol", s.client.Protocol)
}

// clientEnv is the reload env entry for the client identity; "" before initialize.
func (s *Server) clientEnv() string {
	if s.client.Name == "" && s.client.Protocol == "" {
		return ""
	}
	b, err := json.Marshal(s.client)
	if err != nil {
		return ""
	}
	return envClient + "=" + string(b)
}

// takeReloadedClient adopts the identity a reloaded process inherited and
// clears it so children don't inherit it.
func (s *Server) takeReloadedClient() {
	if v := os.Getenv(envClient); v != "" && s.Reloaded && s.client.Name == "" && s.client.Protocol == "" {
		if err := json.Unmarshal([]byte(v), &s.client); err != nil {
			logx.For("mcp").Warn("reloaded client identity", "err", err)
		}
	}
	os.Unsetenv(envClient)
}

// nativePrompt reports whether the client shows its own permission prompt to
// a person for a tool marked requiresUserInteraction: Claude Code from
// claudeCodeMin, which prompts even under bypass and allow rules and denies
// the call when no person can answer.
// A client that elicits gets fleet's dialog instead, so one action is one
// prompt, warnings included.
func (s *Server) nativePrompt() bool {
	p := s.peer()
	return p.Name == "claude-code" && versionAtLeast(p.Version, claudeCodeMin) && !s.elicits()
}

// versionAtLeast compares dotted numeric versions; anything unparsable is false.
func versionAtLeast(v, floor string) bool {
	v, _, _ = strings.Cut(v, "-")
	a, b := strings.Split(v, "."), strings.Split(floor, ".")
	for i := range b {
		if i >= len(a) {
			return false
		}
		x, err1 := strconv.Atoi(a[i])
		y, err2 := strconv.Atoi(b[i])
		if err1 != nil || err2 != nil {
			return false
		}
		if x != y {
			return x > y
		}
	}
	return true
}

// gate describes one confirmation: what a refusal says and records.
type gate struct {
	tool  string // the tool asking
	agent string // a refusal is recorded as this agent's KindConfirmRefused intervention; "" records none
	what  string // the intervention's detail
	why   string // said after "not confirmed: "
	// warn is a warning the user must see before saying yes (uncommitted
	// work, a squad left running): it ends the refusal, and while there is
	// one the client's own prompt does not stand in for confirmed=true.
	warn string
	err  error  // replaces the default refusal text
	ask  string // the dialog's question for an eliciting client (elicit.go)
}

// refuse records a refused confirmation and returns the error the model gets.
func (s *Server) refuse(g gate) error {
	if g.agent != "" && s.F != nil {
		s.F.Intervene(g.agent, board.KindConfirmRefused, g.what)
	}
	if g.err != nil {
		return g.err
	}
	return fmt.Errorf("not confirmed: %sask the user, then call again with confirmed=true%s", g.why, g.warn)
}

// confirm is nil once the user's yes is in, else the refusal. A client
// that declared elicitation asks the user in its dialog (a modern one
// through MRTR: errInputRequired, then the retry), and confirmed is
// ignored; otherwise the client's own permission prompt already asked
// (nativeGated tools for a nativePrompt client, unless there is a warning
// to show first), or confirmed=true.
func (s *Server) confirm(g gate, a args) error {
	switch {
	case s.elicits() && s.peer().modern():
		if g.err = s.mrtrConfirm(g); g.err == nil || errors.Is(g.err, errInputRequired) {
			return g.err
		}
	case s.elicits():
		if g.err = s.elicit(g); g.err == nil {
			return nil
		}
	case g.warn == "" && nativeGated[g.tool] && s.nativePrompt(), a.Confirmed:
		return nil
	}
	return s.refuse(g)
}

// nativeNote replaces the ask-in-chat sentence of a nativeGated tool's
// description for a nativePrompt client.
const (
	askInChat  = "Ask the user first; set confirmed=true only after an explicit yes"
	nativeNote = "Claude Code asks the user to approve every call itself, so call it without asking in chat and without confirmed; " +
		"if the reply is a refusal with a warning, tell the user and call again with confirmed=true after an explicit yes"
)

// nativeTool is a tools/list entry with Claude Code's per-tool _meta.
type nativeTool struct{ tool }

// MarshalJSON is tool's (title, annotations) plus _meta.
func (n nativeTool) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal(n.tool)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("tool %s: %w", n.Name, err)
	}
	m["_meta"] = json.RawMessage(`{"anthropic/requiresUserInteraction":true}`)
	return json.Marshal(m)
}

// listTools is the admiral's tools/list: for a nativePrompt client the
// nativeGated tools carry requiresUserInteraction, say so, and don't
// require confirmed.
func (s *Server) listTools() any {
	if s.elicits() {
		return dialogTools()
	}
	if !s.nativePrompt() {
		return toolDefs
	}
	out := make([]any, len(toolDefs))
	for i, t := range toolDefs {
		if !nativeGated[t.Name] {
			out[i] = t
			continue
		}
		t.Description = strings.Replace(t.Description, askInChat, nativeNote, 1)
		t.InputSchema = maps.Clone(t.InputSchema)
		if req, ok := t.InputSchema["required"].([]string); ok {
			t.InputSchema["required"] = slices.DeleteFunc(slices.Clone(req), func(r string) bool { return r == "confirmed" })
		}
		out[i] = nativeTool{t}
	}
	return out
}

// dialogNotes rewrite every ask-in-chat sentence for a client that gets
// fleet's dialog, so the admiral stops asking in chat first.
var dialogNotes = strings.NewReplacer(
	askInChat, "fleet asks the user in a dialog itself, so call it without asking in chat and without confirmed",
	"ask the user first, confirmed=true only after an explicit yes", "fleet asks the user in a dialog itself, so don't ask in chat first",
	"so it needs confirmed=true", "so fleet asks the user in a dialog first",
	"need confirmed=true (Enter would answer it)", "need confirmed=true, and fleet then asks the user in a dialog (Enter would answer it)",
	"skipped unless confirmed=true", "skipped unless confirmed=true, and then fleet asks the user in a dialog",
)

// dialogConfirmed is the confirmed property for such a client.
var dialogConfirmed = map[string]any{"type": "boolean", "description": "not a yes: fleet asks the user in a dialog"}

// dialogTools is toolDefs for a client that elicits: no description asks in
// chat and confirmed is never required.
func dialogTools() []tool {
	out := slices.Clone(toolDefs)
	for i, t := range out {
		props, ok := t.InputSchema["properties"].(map[string]any)
		if _, has := props["confirmed"]; !ok || !has {
			continue
		}
		t.Description = dialogNotes.Replace(t.Description)
		t.InputSchema = maps.Clone(t.InputSchema)
		props = maps.Clone(props)
		props["confirmed"] = dialogConfirmed
		t.InputSchema["properties"] = props
		if req, ok := t.InputSchema["required"].([]string); ok {
			t.InputSchema["required"] = slices.DeleteFunc(slices.Clone(req), func(r string) bool { return r == "confirmed" })
		}
		out[i] = t
	}
	return out
}
