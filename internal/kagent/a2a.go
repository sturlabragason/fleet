// Package kagent runs a fleet agent as a Session of a kagent Agent in a
// Kubernetes cluster (DESIGN §7d, ADR-145). `fleet kagent acp` is an ACP
// adapter, like claude-agent-acp (ADR-76): fleet's acp-host speaks ACP to it
// on stdio and it speaks A2A v1 JSON-RPC to kagent's gateway, reached through
// `kubectl port-forward` (a companion) or a URL. Fleet writes nothing to the
// cluster: the Agent and its Harness are the user's.
package kagent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Gateway is one kagent Agent's A2A JSON-RPC endpoint.
type Gateway struct {
	URL   string // base URL of the gateway, e.g. http://127.0.0.1:8083
	Agent string // "namespace/name"
	User  string // X-User-Id, for kagent's default (header) authentication
	Token string // bearer token, for TokenReview or OIDC authentication
	HTTP  *http.Client
}

// Part is one A2A message or artifact part: text, or data such as kagent's
// tool calls, which Metadata["kagent.dev/a2a/part-type"] names.
type Part struct {
	Text     string          `json:"text,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
	Metadata map[string]any  `json:"metadata,omitempty"`
}

// Message is an A2A message.
type Message struct {
	MessageID string `json:"messageId"`
	ContextID string `json:"contextId,omitempty"`
	TaskID    string `json:"taskId,omitempty"`
	Role      string `json:"role"`
	Parts     []Part `json:"parts"`
}

// Status is a task's state and its latest message.
type Status struct {
	State   string   `json:"state"`
	Message *Message `json:"message,omitempty"`
}

// Artifact is a task output.
type Artifact struct {
	ArtifactID string `json:"artifactId"`
	Parts      []Part `json:"parts"`
}

// Task is an A2A task.
type Task struct {
	ID        string     `json:"id"`
	ContextID string     `json:"contextId"`
	Status    Status     `json:"status"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

// Event is one streamed response: one field is set.
type Event struct {
	Task           *Task           `json:"task,omitempty"`
	Message        *Message        `json:"message,omitempty"`
	StatusUpdate   *StatusUpdate   `json:"statusUpdate,omitempty"`
	ArtifactUpdate *ArtifactUpdate `json:"artifactUpdate,omitempty"`
}

// StatusUpdate is a task status change.
type StatusUpdate struct {
	TaskID    string `json:"taskId"`
	ContextID string `json:"contextId"`
	Status    Status `json:"status"`
}

// ArtifactUpdate is a new artifact, or with Append more of one: kagent
// streams a reply as one artifact in many appended chunks.
type ArtifactUpdate struct {
	TaskID    string   `json:"taskId"`
	ContextID string   `json:"contextId"`
	Artifact  Artifact `json:"artifact"`
	Append    bool     `json:"append,omitempty"`
}

// State is a task state without its TASK_STATE_ prefix, lower case, with
// the A2A 0.3 spellings folded in: "working", "completed", "canceled", ...
func State(s string) string {
	s = strings.ToLower(strings.TrimPrefix(strings.ToUpper(s), "TASK_STATE_"))
	s = strings.ReplaceAll(s, "_", "-")
	if s == "cancelled" {
		return "canceled"
	}
	return s
}

// Text joins parts' text. kagent splits a reply into many text parts, so
// they join without a separator.
func Text(parts []Part) string {
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(p.Text)
	}
	return sb.String()
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// NewID is a random id for messages and requests; tests swap it.
var NewID = func() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (g Gateway) post(ctx context.Context, method string, params any, stream bool) (*http.Response, error) {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: NewID(), Method: method, Params: params})
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", method, err)
	}
	url := strings.TrimRight(g.URL, "/") + "/agents/" + g.Agent
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("kagent %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("A2A-Version", "1.0")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if g.User != "" {
		req.Header.Set("X-User-Id", g.User)
	}
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	c := g.HTTP
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kagent %s: %w", method, err)
	}
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("kagent %s: HTTP %d: %s", method, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return resp, nil
}

// Send sends text to the conversation contextID ("" starts one) with
// SendStreamingMessage and calls on for every event until the stream ends;
// taskID answers a task that waits for input. A gateway that answers with
// plain JSON gives one event.
func (g Gateway) Send(ctx context.Context, contextID, taskID, text string, on func(Event)) error {
	msg := Message{MessageID: NewID(), ContextID: contextID, TaskID: taskID, Role: "ROLE_USER", Parts: []Part{{Text: text}}}
	resp, err := g.post(ctx, "SendStreamingMessage", map[string]any{"message": msg}, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if err != nil {
			return fmt.Errorf("kagent send: %w", err)
		}
		return deliver(b, on)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	var data []byte
	for sc.Scan() {
		line := sc.Bytes()
		if d, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			data = append(data, bytes.TrimSpace(d)...)
			continue
		}
		if len(line) == 0 && len(data) > 0 {
			if err := deliver(data, on); err != nil {
				return err
			}
			data = nil
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("kagent stream: %w", err)
	}
	if len(data) > 0 {
		return deliver(data, on)
	}
	return nil
}

// deliver decodes one JSON-RPC response into an event.
func deliver(b []byte, on func(Event)) error {
	var r rpcResponse
	if err := json.Unmarshal(b, &r); err != nil {
		return fmt.Errorf("kagent: bad response: %w", err)
	}
	if r.Error != nil {
		return fmt.Errorf("kagent: %s (%d)", r.Error.Message, r.Error.Code)
	}
	ev, err := decodeEvent(r.Result)
	if err != nil {
		return err
	}
	on(ev)
	return nil
}

// decodeEvent reads a v1 StreamResponse, or an A2A 0.3 result with "kind".
func decodeEvent(b json.RawMessage) (Event, error) {
	var ev Event
	if err := json.Unmarshal(b, &ev); err != nil {
		return ev, fmt.Errorf("kagent: bad event: %w", err)
	}
	if ev.Task != nil || ev.Message != nil || ev.StatusUpdate != nil || ev.ArtifactUpdate != nil {
		return ev, nil
	}
	var k struct{ Kind string }
	_ = json.Unmarshal(b, &k)
	var v any
	switch k.Kind {
	case "task":
		ev.Task = &Task{}
		v = ev.Task
	case "message":
		ev.Message = &Message{}
		v = ev.Message
	case "status-update":
		ev.StatusUpdate = &StatusUpdate{}
		v = ev.StatusUpdate
	case "artifact-update":
		ev.ArtifactUpdate = &ArtifactUpdate{}
		v = ev.ArtifactUpdate
	default:
		return ev, nil
	}
	if err := json.Unmarshal(b, v); err != nil {
		return ev, fmt.Errorf("kagent: bad %s event: %w", k.Kind, err)
	}
	return ev, nil
}

// Cancel asks the gateway to cancel a task.
func (g Gateway) Cancel(ctx context.Context, taskID string) error {
	resp, err := g.post(ctx, "CancelTask", map[string]any{"id": taskID}, false)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
