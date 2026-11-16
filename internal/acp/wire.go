// Package acp is fleet's client for the Agent Client Protocol: JSON-RPC 2.0,
// one JSON object per line, over the stdin and stdout of a tool's ACP server
// (`copilot --acp`, `agy acp`). Stdlib only (DESIGN §7d, ADR-74).
//
// This file holds the wire types: the subset of ACP protocol version 1 fleet
// sends or reads, plus Copilot's opt-in session events. Fields fleet does not
// read stay as json.RawMessage so an unknown shape never fails a decode.
package acp

import (
	"encoding/base64"
	"encoding/json"
)

// ProtocolVersion is the ACP version fleet speaks.
const ProtocolVersion = 1

// Method names, client to agent.
const (
	MethodInitialize     = "initialize"
	MethodSessionNew     = "session/new"
	MethodSessionLoad    = "session/load"
	MethodSessionResume  = "session/resume"
	MethodSessionPrompt  = "session/prompt"
	MethodSessionCancel  = "session/cancel" // a notification, no response
	MethodSessionClose   = "session/close"
	MethodSessionSetMode = "session/set_mode"
	// MethodSessionSetModel and MethodSessionSetConfig pick a session's
	// model (ADR-112): the first where the agent sends "models" (Copilot,
	// agy), the second for a "model" config option (claude-agent-acp).
	MethodSessionSetModel  = "session/set_model"
	MethodSessionSetConfig = "session/set_config_option"
)

// Method names, agent to client.
const (
	MethodSessionUpdate     = "session/update" // notification
	MethodRequestPermission = "session/request_permission"
	MethodFsRead            = "fs/read_text_file"  // refused: fleet offers no fs capability
	MethodFsWrite           = "fs/write_text_file" // refused
	MethodCopilotEvent      = "github.com/copilot/sessionEvent"
)

// Message is one JSON-RPC 2.0 frame: a request (ID and Method), a
// notification (Method, no ID) or a response (ID, Result or Error). IDs may
// be numbers or strings on the wire, so they stay raw.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return e.Message }

// JSON-RPC error codes fleet uses.
const (
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
)

// InitializeParams is the client's half of the handshake.
type InitializeParams struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	ClientInfo         *Implementation    `json:"clientInfo,omitempty"`
}

// ClientCapabilities: fleet offers no file system and no terminal, so the
// agent uses its own tools. Meta carries Copilot's event opt-in.
type ClientCapabilities struct {
	FS       FSCapability   `json:"fs"`
	Terminal bool           `json:"terminal"`
	Meta     map[string]any `json:"_meta,omitempty"`
}

// FSCapability says which fs/* methods the client serves.
type FSCapability struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

// Implementation names a client or agent.
type Implementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

// CopilotEvents is the opt-in Copilot reads from _meta in initialize
// (`copilot help acp`): {"github.com/copilot": {"events": CopilotEvents}}.
var CopilotEvents = []string{"assistant.turn_start", "assistant.idle", "session.idle"}

// InitializeResult is the agent's half of the handshake.
type InitializeResult struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AgentInfo         *Implementation   `json:"agentInfo,omitempty"`
	AuthMethods       json.RawMessage   `json:"authMethods,omitempty"`
}

// AgentCapabilities is what fleet reads from initialize: whether a session
// can be loaded (history replayed) or resumed (no replay). Probed 2026-11-10:
// Copilot 1.0.108 has loadSession and close, no resume; agy 1.5.4 has
// resume, load and close.
type AgentCapabilities struct {
	LoadSession         bool                `json:"loadSession"`
	SessionCapabilities SessionCapabilities `json:"sessionCapabilities"`
	PromptCapabilities  PromptCapabilities  `json:"promptCapabilities,omitzero"`
}

// PromptCapabilities says which content blocks a prompt may hold besides
// text and resource_link, which every agent takes. Probed 2026-11-14:
// Copilot 1.0.112, agy 1.5.8 and claude-agent-acp 0.103.0 all take image
// and embeddedContext, none takes audio.
type PromptCapabilities struct {
	Image           bool `json:"image"`
	Audio           bool `json:"audio"`
	EmbeddedContext bool `json:"embeddedContext"`
}

// SessionCapabilities: a non-nil field means the agent offers the method.
type SessionCapabilities struct {
	Resume json.RawMessage `json:"resume,omitempty"`
	Load   json.RawMessage `json:"load,omitempty"`
	Close  json.RawMessage `json:"close,omitempty"`
	List   json.RawMessage `json:"list,omitempty"`
}

// CanResume reports whether session/resume is offered (agy 1.5+).
func (a AgentCapabilities) CanResume() bool { return a.SessionCapabilities.Resume != nil }

// CanLoad reports whether session/load is offered (Copilot, agy).
func (a AgentCapabilities) CanLoad() bool {
	return a.LoadSession || a.SessionCapabilities.Load != nil
}

// CanClose reports whether session/close is offered.
func (a AgentCapabilities) CanClose() bool { return a.SessionCapabilities.Close != nil }

// MCPServer is a stdio MCP server handed to a new session (the fleet peer
// server, DESIGN §6.6).
type MCPServer struct {
	Name    string        `json:"name"`
	Command string        `json:"command"`
	Args    []string      `json:"args"`
	Env     []EnvVariable `json:"env"`
}

// EnvVariable is one environment variable for an MCP server.
type EnvVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// NewSessionParams starts a session in cwd.
type NewSessionParams struct {
	Cwd        string      `json:"cwd"`
	MCPServers []MCPServer `json:"mcpServers"`
	// Meta is the agent-specific _meta object, e.g. claude-agent-acp's
	// {"claudeCode":{"options":{...}}} (ADR-99).
	Meta json.RawMessage `json:"_meta,omitempty"`
}

// NewSessionResult carries the new session's id.
type NewSessionResult struct {
	SessionID string `json:"sessionId"`
}

// SessionModelState is what a session/new, load or resume result says about
// models: the "models" object (unstable ACP: Copilot, agy) and the config
// options, one of which may be the model (category "model": claude-agent-acp).
type SessionModelState struct {
	Models        *ModelState    `json:"models,omitempty"`
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`
}

// ModelState is the session's current model and the ones it may switch to.
type ModelState struct {
	CurrentModelID  string      `json:"currentModelId"`
	AvailableModels []ModelInfo `json:"availableModels"`
}

// ModelInfo is one model an agent offers.
type ModelInfo struct {
	ModelID     string `json:"modelId"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// ConfigOption is one session config option; fleet reads only selects.
type ConfigOption struct {
	ID           string `json:"id"`
	Category     string `json:"category,omitempty"`
	Type         string `json:"type,omitempty"`
	CurrentValue any    `json:"currentValue,omitempty"`
	// Options may be values or groups of values; only a flat list of
	// values is read (see SessionModelState.models).
	Options json.RawMessage `json:"options,omitempty"`
}

// ConfigChoice is one value of a select config option.
type ConfigChoice struct {
	Value       string `json:"value"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// SetModelParams picks a session's model (session/set_model).
type SetModelParams struct {
	SessionID string `json:"sessionId"`
	ModelID   string `json:"modelId"`
}

// SetConfigParams sets one config option (session/set_config_option).
type SetConfigParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     string `json:"value"`
}

// SessionRefParams is the params of session/load and session/resume.
type SessionRefParams struct {
	SessionID  string          `json:"sessionId"`
	Cwd        string          `json:"cwd"`
	MCPServers []MCPServer     `json:"mcpServers"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

// SetModeParams switches a session's mode (session/set_mode).
type SetModeParams struct {
	SessionID string `json:"sessionId"`
	ModeID    string `json:"modeId"`
}

// SessionIDParams is the params of session/cancel and session/close.
type SessionIDParams struct {
	SessionID string `json:"sessionId"`
}

// ContentBlock is one piece of content. Fleet draws text only and sends
// text, image and resource_link blocks; other kinds are kept by type name.
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"` // image
	Data     string `json:"data,omitempty"`     // image: base64
	URI      string `json:"uri,omitempty"`      // image (optional), resource_link
	Name     string `json:"name,omitempty"`     // resource_link
}

// TextBlock is a text content block.
func TextBlock(s string) ContentBlock { return ContentBlock{Type: "text", Text: s} }

// ImageBlock is an image content block: data is the raw image, sent
// base64; uri names where it came from.
func ImageBlock(mime string, data []byte, uri string) ContentBlock {
	return ContentBlock{Type: "image", MimeType: mime, Data: base64.StdEncoding.EncodeToString(data), URI: uri}
}

// ResourceLinkBlock points the agent at a resource, e.g. a file:// URI it
// reads with its own tools.
func ResourceLinkBlock(uri, name string) ContentBlock {
	return ContentBlock{Type: "resource_link", URI: uri, Name: name}
}

// PromptParams sends the user's turn.
type PromptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

// PromptResult ends a turn. Usage is the turn's token use, when the agent
// reports it (agy does; ACP's unstable usage field).
type PromptResult struct {
	StopReason StopReason `json:"stopReason"`
	Usage      *Usage     `json:"usage,omitempty"`
}

// Usage is the tokens one turn used. agy counts cached tokens apart from
// InputTokens, and thought tokens inside OutputTokens.
type Usage struct {
	InputTokens       int64 `json:"inputTokens"`
	OutputTokens      int64 `json:"outputTokens"`
	CachedReadTokens  int64 `json:"cachedReadTokens,omitempty"`
	CachedWriteTokens int64 `json:"cachedWriteTokens,omitempty"`
	ThoughtTokens     int64 `json:"thoughtTokens,omitempty"`
	TotalTokens       int64 `json:"totalTokens,omitempty"`
}

// StopReason says why a turn ended.
type StopReason string

const (
	StopEndTurn         StopReason = "end_turn"
	StopMaxTokens       StopReason = "max_tokens"
	StopMaxTurnRequests StopReason = "max_turn_requests"
	StopRefusal         StopReason = "refusal"
	StopCancelled       StopReason = "cancelled"
)

// SessionNotification is the params of session/update.
type SessionNotification struct {
	SessionID string        `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}

// SessionUpdate kinds fleet draws (the `sessionUpdate` discriminator).
const (
	UpdateUserMessage    = "user_message_chunk"
	UpdateAgentMessage   = "agent_message_chunk"
	UpdateAgentThought   = "agent_thought_chunk"
	UpdateToolCall       = "tool_call"
	UpdateToolCallUpdate = "tool_call_update"
	UpdatePlan           = "plan"
	UpdateModeChange     = "current_mode_update"
	UpdateUsage          = "usage_update" // context fill and cost (ACP unstable)
)

// SessionUpdate is one update, flattened over its kinds. Content is a single
// block for message chunks and a list for tool calls, so it stays raw; use
// Text to read it.
type SessionUpdate struct {
	Kind       string          `json:"sessionUpdate"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	Title      string          `json:"title,omitempty"`
	ToolKind   string          `json:"kind,omitempty"`
	Status     string          `json:"status,omitempty"` // pending, in_progress, completed, failed
	Entries    json.RawMessage `json:"entries,omitempty"`
	ModeID     string          `json:"currentModeId,omitempty"`
	// Name and RawInput are a tool call's tool name (an agy extension) and
	// its arguments.
	Name     string          `json:"name,omitempty"`
	RawInput json.RawMessage `json:"rawInput,omitempty"`
	// Used and Size are a usage_update's context tokens and window, Cost
	// its session cost so far; Meta carries claude-agent-acp's
	// "_claude/rateLimit" and "_claude/model".
	Used int64           `json:"used,omitempty"`
	Size int64           `json:"size,omitempty"`
	Cost *Cost           `json:"cost,omitempty"`
	Meta json.RawMessage `json:"_meta,omitempty"`
}

// Cost is a usage_update's cumulative session cost.
type Cost struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// RateLimit is one subscription window claude-agent-acp forwards from
// Claude Code's rate_limit_event (SDKRateLimitInfo).
type RateLimit struct {
	Type        string  `json:"rateLimitType"` // five_hour, seven_day, seven_day_opus, ...
	Utilization float64 `json:"utilization"`   // used: 0..1, or percent above 1
	ResetsAt    int64   `json:"resetsAt"`      // unix seconds
}

// UsageMeta reads a usage_update's _meta: the rate-limit window it
// carries (nil when none) and the model the usage belongs to.
func (u SessionUpdate) UsageMeta() (rl *RateLimit, model string) {
	var m struct {
		RL    *RateLimit `json:"_claude/rateLimit"`
		Model string     `json:"_claude/model"`
	}
	_ = json.Unmarshal(u.Meta, &m) // a _meta fleet can't read is no _meta
	return m.RL, m.Model
}

// SubagentTool is the tool agy starts a subagent with.
const SubagentTool = "invoke_subagent"

// Subagent returns the name of the subagent a tool call starts (its role,
// else its type), and false when the call starts none.
func (u SessionUpdate) Subagent() (string, bool) {
	if u.Name != SubagentTool {
		return "", false
	}
	var in struct{ Role, TypeName string }
	_ = json.Unmarshal(u.RawInput, &in)
	switch {
	case in.Role != "":
		return in.Role, true
	case in.TypeName != "":
		return in.TypeName, true
	}
	return "subagent", true
}

// Text returns the text of the update's content: one text block, or every
// text block of a tool call's content list, joined by newlines.
func (u SessionUpdate) Text() string {
	if len(u.Content) == 0 {
		return ""
	}
	var one ContentBlock
	if err := json.Unmarshal(u.Content, &one); err == nil && one.Type != "" {
		if one.Type == "image" {
			return "[image]" // a replayed prompt's attachment
		}
		return one.Text
	}
	var many []struct {
		Type    string       `json:"type"`
		Content ContentBlock `json:"content"`
		Text    string       `json:"text"`
	}
	if err := json.Unmarshal(u.Content, &many); err != nil {
		return ""
	}
	out := ""
	for _, m := range many {
		t := m.Text
		if t == "" {
			t = m.Content.Text
		}
		if t == "" {
			continue
		}
		if out != "" {
			out += "\n"
		}
		out += t
	}
	return out
}

// RequestPermissionParams is the agent asking before a tool call.
type RequestPermissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  ToolCallRef        `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

// ToolCallRef names the tool call a permission request is about.
type ToolCallRef struct {
	ToolCallID string `json:"toolCallId"`
	Title      string `json:"title,omitempty"`
	Kind       string `json:"kind,omitempty"`
}

// PermissionOption is one choice the agent offers.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // allow_once, allow_always, reject_once, reject_always
}

// RequestPermissionResult is the client's answer.
type RequestPermissionResult struct {
	Outcome PermissionOutcome `json:"outcome"`
}

// PermissionOutcome is "selected" with an option id, or "cancelled" (the
// turn was cancelled while the question was open).
type PermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

// Selected answers a permission request with one option.
func Selected(optionID string) RequestPermissionResult {
	return RequestPermissionResult{Outcome: PermissionOutcome{Outcome: "selected", OptionID: optionID}}
}

// CancelledOutcome answers a permission request whose turn was cancelled.
func CancelledOutcome() RequestPermissionResult {
	return RequestPermissionResult{Outcome: PermissionOutcome{Outcome: "cancelled"}}
}

// CopilotEvent is the params of github.com/copilot/sessionEvent. AgentID set
// means a subagent's event, not the foreground agent's.
type CopilotEvent struct {
	SessionID string          `json:"sessionId"`
	Type      string          `json:"type"`
	AgentID   string          `json:"agentId,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// Aborted reports data.aborted on an assistant.idle event.
func (e CopilotEvent) Aborted() bool {
	var d struct {
		Aborted bool `json:"aborted"`
	}
	_ = json.Unmarshal(e.Data, &d)
	return d.Aborted
}
