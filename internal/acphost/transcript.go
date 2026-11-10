package acphost

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// transcript appends Entries to the agent's transcript, one JSON line each.
type transcript struct {
	mu sync.Mutex
	f  *os.File
}

func openTranscript(path string) (*transcript, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &transcript{f: f}, nil
}

// add stamps e (when At is zero) and appends it.
func (t *transcript) add(e Entry) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err = t.f.Write(append(b, '\n'))
	return err
}

func (t *transcript) close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.f.Close()
}

// tailBlock is how much of the file Tail and LastSession read per step, from
// the end backwards, so a long transcript is never read whole.
const tailBlock = 32 << 10

// eachFromEnd calls fn with the transcript's entries newest first until fn
// returns false. Lines that don't decode are skipped.
func eachFromEnd(path string, fn func(Entry) bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	off := st.Size()
	var rest []byte // the partial first line of the block read last
	for off > 0 {
		n := int64(tailBlock)
		if n > off {
			n = off
		}
		off -= n
		buf := make([]byte, n, int(n)+len(rest))
		if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
			return err
		}
		buf = append(buf, rest...)
		lines := bytes.Split(buf, []byte{'\n'})
		start := 0
		if off > 0 {
			rest = lines[0]
			start = 1
		} else {
			rest = nil
		}
		for i := len(lines) - 1; i >= start; i-- {
			if len(bytes.TrimSpace(lines[i])) == 0 {
				continue
			}
			var e Entry
			if json.Unmarshal(lines[i], &e) != nil {
				continue
			}
			if !fn(e) {
				return nil
			}
		}
	}
	return nil
}

// Tail returns the transcript's last n entries, oldest first. A missing or
// unreadable file gives none.
func Tail(path string, n int) []Entry {
	if n <= 0 {
		return nil
	}
	var rev []Entry
	_ = eachFromEnd(path, func(e Entry) bool {
		rev = append(rev, e)
		return len(rev) < n
	})
	if len(rev) == 0 {
		return nil
	}
	out := make([]Entry, len(rev))
	for i, e := range rev {
		out[len(rev)-1-i] = e
	}
	return out
}

// LastSession returns the session id of the transcript's newest KindSession
// entry, or "" when there is none.
func LastSession(path string) string {
	id := ""
	_ = eachFromEnd(path, func(e Entry) bool {
		if e.Kind == KindSession && e.SessionID != "" {
			id = e.SessionID
			return false
		}
		return true
	})
	return id
}

// LastLine returns the text of the newest entry worth showing on a tile:
// agent text, a question, a tool call or an error, as one short line.
func LastLine(path string) string {
	line := ""
	_ = eachFromEnd(path, func(e Entry) bool {
		switch e.Kind {
		case KindAgent, KindPermission, KindTool, KindError, KindUser:
			line = summary(e)
			return line == ""
		}
		return true
	})
	return line
}

// LastAgentText returns the text of the transcript's newest agent message,
// or "" when there is none.
func LastAgentText(path string) string {
	text := ""
	_ = eachFromEnd(path, func(e Entry) bool {
		if e.Kind == KindAgent && strings.TrimSpace(e.Text) != "" {
			text = e.Text
			return false
		}
		return true
	})
	return text
}
