package status

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

// LastMessageTail is how much of a session log Replies and ClaudeReplies read.
var LastMessageTail int64 = 1 << 20

// Reply is one main-agent reply in a tool's session log.
type Reply struct {
	Text string
	T    time.Time
}

// Replies returns the main-agent assistant.message contents in a Copilot
// events.jsonl, oldest first, reading only the last LastMessageTail bytes.
// Subagent messages (data.agentId or data.parentToolCallId set) and empty
// ones are skipped.
func Replies(path string) ([]Reply, error) {
	var out []Reply
	err := scanTail(path, func(line []byte) {
		if !bytes.Contains(line, []byte(`"assistant.message"`)) {
			return
		}
		var e struct {
			Type      string    `json:"type"`
			Timestamp time.Time `json:"timestamp"`
			Data      struct {
				Content          string `json:"content"`
				AgentID          string `json:"agentId"`
				ParentToolCallID string `json:"parentToolCallId"`
			} `json:"data"`
		}
		if json.Unmarshal(line, &e) == nil && e.Type == "assistant.message" &&
			e.Data.AgentID == "" && e.Data.ParentToolCallID == "" && strings.TrimSpace(e.Data.Content) != "" {
			out = append(out, Reply{e.Data.Content, e.Timestamp})
		}
	})
	return out, err
}

// ClaudeReplies returns the main-agent assistant replies in a Claude Code
// session .jsonl, oldest first, reading only the last LastMessageTail bytes.
// Claude writes one line per content block, so the text blocks of one
// message are joined. Sidechain (subagent) lines, thinking and tool_use
// blocks are skipped.
func ClaudeReplies(path string) ([]Reply, error) {
	var out []Reply
	lastID := ""
	err := scanTail(path, func(line []byte) {
		if !bytes.Contains(line, []byte(`"assistant"`)) || !bytes.Contains(line, []byte(`"text"`)) {
			return
		}
		var e struct {
			Type        string    `json:"type"`
			Timestamp   time.Time `json:"timestamp"`
			IsSidechain bool      `json:"isSidechain"`
			Message     struct {
				ID      string `json:"id"`
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &e) != nil || e.Type != "assistant" || e.IsSidechain || e.Message.Role != "assistant" {
			return
		}
		var text []string
		for _, c := range e.Message.Content {
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				text = append(text, c.Text)
			}
		}
		if len(text) == 0 {
			return
		}
		if e.Message.ID != "" && e.Message.ID == lastID && len(out) > 0 {
			out[len(out)-1].Text += "\n\n" + strings.Join(text, "\n\n")
			return
		}
		out, lastID = append(out, Reply{strings.Join(text, "\n\n"), e.Timestamp}), e.Message.ID
	})
	return out, err
}
