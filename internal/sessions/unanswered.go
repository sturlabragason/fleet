package sessions

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// tailRead is how much of the end of a transcript Unanswered reads.
var tailRead int64 = 1 << 20

// Unanswered returns the user messages that came after the assistant's last
// step (a reply or a tool call) in the session at path (a Copilot session
// folder or a Claude transcript), oldest first: messages the model never
// saw, such as one queued while it was calling fleet_handoff. A restart that
// kills the session drops them unless the next admiral is told (ADR-196).
func Unanswered(path string) []string {
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if st.IsDir() {
		return unanswered(tail(filepath.Join(path, "events.jsonl")), copilotTurn)
	}
	return unanswered(tail(path), claudeTurn)
}

// turn classifies one transcript line: user text, or reply=true for an
// assistant step.
type turn func(line []byte) (user string, reply bool)

func unanswered(buf []byte, fn turn) []string {
	var out []string
	for l := range bytes.SplitSeq(buf, []byte("\n")) {
		user, reply := fn(l)
		switch {
		case reply:
			out = out[:0]
		case user != "":
			out = append(out, user)
		}
	}
	return out
}

func copilotTurn(l []byte) (string, bool) {
	var e struct {
		Type string
		Data struct{ Content any }
	}
	if !bytes.Contains(l, []byte(`.message"`)) || json.Unmarshal(l, &e) != nil {
		return "", false
	}
	c, _ := e.Data.Content.(string)
	c = strings.TrimSpace(c)
	switch e.Type {
	case "user.message":
		return c, false
	case "assistant.message":
		return "", true
	}
	return "", false
}

func claudeTurn(l []byte) (string, bool) {
	var e struct {
		Type    string
		IsMeta  bool
		Message struct{ Content json.RawMessage }
	}
	if json.Unmarshal(l, &e) != nil {
		return "", false
	}
	switch e.Type {
	case "user":
		if e.IsMeta {
			return "", false
		}
		if t := userText(e.Message.Content); !strings.HasPrefix(t, "[Request interrupted") {
			return t, false
		}
	case "assistant":
		return "", true
	}
	return "", false
}

// tail is the last tailRead bytes of path without the partial first line.
func tail(path string) []byte {
	fh, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return nil
	}
	off := max(st.Size()-tailRead, 0)
	buf := make([]byte, st.Size()-off)
	if _, err := fh.ReadAt(buf, off); err != nil && err != io.EOF {
		return nil
	}
	if off > 0 {
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	return buf
}
