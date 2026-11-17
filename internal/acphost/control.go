// Package acphost runs one ACP agent: `fleet acp-host <name>` lives in the
// agent's tmux window, starts the tool's ACP server as its child, speaks ACP
// to it through internal/acp, writes the agent's status file, appends its
// transcript and serves a control socket (DESIGN §7d, ADR-74).
//
// This file is the contract with the rest of fleet: where the socket and the
// transcript live, the control request and response, and the transcript
// entry. internal/fleet only uses what is here.
package acphost

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"time"

	"github.com/sturlabragason/fleet/internal/acp"
)

// Dir is where ACP hosts keep their sockets and transcripts.
func Dir(home string) string { return filepath.Join(home, "acp") }

// SocketPath is the agent's control socket.
func SocketPath(home, name string) string { return filepath.Join(Dir(home), name+".sock") }

// TranscriptPath is the agent's transcript, one Entry per line.
func TranscriptPath(home, name string) string { return filepath.Join(Dir(home), name+".jsonl") }

// PromptPath holds the prompts an agent's host has not sent yet: spawn's
// first prompt, and whatever a host that died before its session was ready
// had queued. The next host sends them first (--prompt-file).
func PromptPath(home, name string) string { return filepath.Join(Dir(home), name+".prompt") }

// Control operations.
const (
	OpPrompt = "prompt" // Text: send as a prompt (held until the running turn ends)
	OpAnswer = "answer" // Option: 1-based option number, or OptionID
	OpCancel = "cancel" // cancel the running turn (session/cancel)
	// OpSendNow sends Text (optional) and the held prompts now as one
	// prompt, cancelling a running turn first (ADR-152).
	OpSendNow = "send-now"
	// OpSteer delivers Text to the running turn now and says how in
	// Response.Delivery; Steer is the profile's acp_steer (ADR-158).
	OpSteer = "steer"
	OpState = "state" // no change; the response carries the host's state
	OpStop  = "stop"  // close the session, stop the child and exit
	// OpTake hands over the prompts held during the running turn and drops
	// them from the queue: an agent blocked in fleet_wait reads them there
	// instead of after a turn that will not end on its own.
	OpTake = "take"
)

// Request is one control request, one JSON line on the socket.
type Request struct {
	Op       string `json:"op"`
	Text     string `json:"text,omitempty"`
	Option   int    `json:"option,omitempty"`
	OptionID string `json:"option_id,omitempty"`
	Steer    string `json:"steer,omitempty"` // OpSteer: SteerPrompt, SteerCancel or ""
}

// How a tool takes a message mid-turn without acp.MethodSteer: the
// profile's acp_steer (ADR-158).
const (
	SteerPrompt = "prompt" // a second session/prompt joins the running turn (agy)
	SteerCancel = "cancel" // cancel the turn and send the text at once (default)
)

// InterruptNote opens a steer that had to stop the running turn, so the
// agent knows why its turn ended and what may be half done (ADR-158).
const InterruptNote = "[fleet] Your previous turn was stopped to deliver this steer. " +
	"What it already did is still in the conversation; a step that was running when it " +
	"stopped may not have finished, so check it before you rely on it. Then continue with this:\n\n"

// What OpPrompt or OpSteer did with the text (Response.Delivery).
const (
	DeliverySteered     = "steered"     // added to the running turn; nothing stopped
	DeliveryInterrupted = "interrupted" // the turn was cancelled and the text sent at once
	DeliverySent        = "sent"        // no turn ran: sent as a new prompt
	DeliveryQueued      = "queued"      // held until the session is ready or the turn returns
)

// Response answers one Request, one JSON line.
type Response struct {
	OK        bool      `json:"ok"`
	Err       string    `json:"err,omitempty"`
	Status    string    `json:"status,omitempty"` // working, waiting, idle, error
	SessionID string    `json:"session_id,omitempty"`
	Question  *Question `json:"question,omitempty"` // the open permission request
	Queued    []string  `json:"queued,omitempty"`   // OpTake: the prompts taken, oldest first
	Delivery  string    `json:"delivery,omitempty"` // OpPrompt, OpSteer: one of the Delivery values
}

// Question is an open permission request as fleet shows it.
type Question struct {
	Title   string   `json:"title"`
	Options []Option `json:"options"`
}

// Option is one answer, numbered from 1 in the order the agent sent them.
type Option struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Transcript entry kinds.
const (
	KindUser       = "user"       // a prompt fleet sent
	KindAgent      = "agent"      // agent message text (chunks joined per message)
	KindThought    = "thought"    // agent thought text
	KindTool       = "tool"       // a tool call or its update: Title, Status
	KindPermission = "permission" // a question: Title, Options
	KindAnswer     = "answer"     // the option chosen, or "cancelled"
	KindStatus     = "status"     // a status change: Status
	KindError      = "error"      // the child exited, a protocol error
	KindSession    = "session"    // session started/loaded/resumed: SessionID
	KindUsage      = "usage"      // a turn's token use, when the agent reports it: Usage
	KindCancel     = "cancel"     // a cancel of the running turn, from the pane
)

// ByPane marks an entry the user made by typing in the agent's pane
// (Entry.By): fleet reads it as the user's key press (ADR-82).
const ByPane = "pane"

// Entry is one transcript line.
type Entry struct {
	At        time.Time `json:"at"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text,omitempty"`
	Title     string    `json:"title,omitempty"`
	Status    string    `json:"status,omitempty"`
	ToolID    string    `json:"tool_id,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	Options   []Option  `json:"options,omitempty"`
	// Sub names the subagent a tool call starts (agy's invoke_subagent).
	Sub   string     `json:"sub,omitempty"`
	Usage *acp.Usage `json:"usage,omitempty"`
	// By is ByPane for a prompt, answer or cancel typed in the pane;
	// empty for fleet's own (control socket).
	By string `json:"by,omitempty"`
}

// ErrNoHost means nothing listens on the agent's socket.
var ErrNoHost = errors.New("acp host not running")

// Call sends one request to the agent's host and returns its response. A
// response with OK false comes back as an error carrying Err.
func Call(home, name string, req Request, timeout time.Duration) (Response, error) {
	conn, err := net.DialTimeout("unix", SocketPath(home, name), timeout)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %w", ErrNoHost, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	b, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return Response{}, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return Response{}, err
	}
	if !resp.OK {
		return resp, errors.New(resp.Err)
	}
	return resp, nil
}
