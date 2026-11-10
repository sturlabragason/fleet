package status

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

// boxChars are box-drawing and block characters; a line made only of these
// (and spaces) is chrome.
const boxChars = "─━═│┃║╭╮╰╯╻╹╺╸╴╶▄▀▔▁▂▌▐█░▒▓┌┐└┘├┤┬┴┼╌╍┄┅┈┉╔╗╚╝╠╣╦╩╬"

// borders are stripped from both ends of a line.
const borders = "│┃║"

// CleanScreen drops pane chrome for a reader: lines made only of box or
// block characters, left/right borders and the transcript scrollbar,
// trailing spaces and repeated blank lines.
func CleanScreen(text string) string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		l = stripBorders(l)
		if strings.Trim(l, boxChars+" ") == "" {
			l = ""
		}
		if l == "" && (len(out) == 0 || out[len(out)-1] == "") {
			continue
		}
		out = append(out, l)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

func stripBorders(l string) string {
	for {
		t := strings.TrimRight(l, " ")
		if r := strings.TrimRight(t, borders); r != t {
			l = r
			continue
		}
		s := strings.TrimLeft(t, " ")
		if r := strings.TrimLeft(s, borders); r != s {
			l = strings.TrimPrefix(r, " ")
			continue
		}
		return t
	}
}

// LastMessageTail is how much of the event log LastMessage reads.
var LastMessageTail int64 = 1 << 20

// LastMessage returns the content of the last main-agent assistant.message
// in a Copilot events.jsonl, reading only the last LastMessageTail bytes.
// Subagent messages (data.agentId or data.parentToolCallId set) and empty
// ones are skipped. ok=false when none is found.
func LastMessage(path string) (string, bool, error) {
	last, found := "", false
	err := scanTail(path, func(line []byte) {
		if !bytes.Contains(line, []byte(`"assistant.message"`)) {
			return
		}
		var e struct {
			Type string `json:"type"`
			Data struct {
				Content          string `json:"content"`
				AgentID          string `json:"agentId"`
				ParentToolCallID string `json:"parentToolCallId"`
			} `json:"data"`
		}
		if json.Unmarshal(line, &e) == nil && e.Type == "assistant.message" &&
			e.Data.AgentID == "" && e.Data.ParentToolCallID == "" && strings.TrimSpace(e.Data.Content) != "" {
			last, found = e.Data.Content, true
		}
	})
	return last, found && err == nil, err
}

// LastClaudeMessage returns the text of the last main-agent assistant reply
// in a Claude Code session .jsonl, reading only the last LastMessageTail
// bytes. Claude writes one line per content block, so the text blocks of
// that one message are joined. Sidechain (subagent) lines, thinking and
// tool_use blocks are skipped. ok=false when none is found.
func LastClaudeMessage(path string) (string, bool, error) {
	var parts []string
	lastID := ""
	err := scanTail(path, func(line []byte) {
		if !bytes.Contains(line, []byte(`"assistant"`)) || !bytes.Contains(line, []byte(`"text"`)) {
			return
		}
		var e struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
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
		if e.Message.ID == "" || e.Message.ID != lastID {
			parts = nil
		}
		parts, lastID = append(parts, text...), e.Message.ID
	})
	if err != nil || len(parts) == 0 {
		return "", false, err
	}
	return strings.Join(parts, "\n\n"), true, nil
}

// scanTail calls fn with each whole line in the last LastMessageTail bytes
// of the file at path.
func scanTail(path string, fn func(line []byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	off := fi.Size() - LastMessageTail
	if off < 0 {
		off = 0
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return err
	}
	r := bufio.NewReaderSize(f, 256<<10)
	if off > 0 { // drop the partial first line
		if _, err := r.ReadBytes('\n'); err != nil {
			return nil
		}
	}
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			fn(line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
