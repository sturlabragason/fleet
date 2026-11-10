package acphost

import (
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// The pane as a small console (ADR-82, ADR-85): the conversation above one
// input line that the user types into (drawn by view.go). The terminal is
// in raw mode, so the host reads key presses, not lines: Enter sends, Esc
// cancels the turn, Ctrl-T hands the session to the tool's own screen,
// PgUp and PgDn scroll the conversation.

// escWait is how long a lone Esc waits for the rest of an escape sequence
// before it counts as the Esc key.
var escWait = 50 * time.Millisecond

const (
	inputPrompt = "› "
	// Bracketed paste on and off: a pasted newline is part of the prompt,
	// not Enter.
	PasteOn  = "\x1b[?2004h"
	PasteOff = "\x1b[?2004l"
)

// keys reads key presses from r until it closes. The screen is drawn once
// the bytes of a read are handled, not per byte, and a bracketed paste is
// collected and lands as one edit (issue #42): drawing per byte made a
// large paste take minutes.
func (h *host) keys(r io.Reader) {
	ch := make(chan byte, 4096)
	var queued atomic.Int64 // bytes read but not handled yet
	go func() {
		defer close(ch)
		b := make([]byte, 4096)
		for {
			n, err := r.Read(b)
			queued.Add(int64(n))
			for _, c := range b[:n] {
				ch <- c
			}
			if err != nil {
				return
			}
		}
	}()
	con := h.con
	var paste []byte // a bracketed paste being read; nil outside one
	next := func() (byte, bool) {
		b, ok := <-ch
		if ok {
			queued.Add(-1)
		}
		return b, ok
	}
	for {
		if queued.Load() == 0 {
			con.flush()
		}
		b, ok := next()
		if !ok {
			return
		}
		switch {
		case b == 0x1b:
			if paste != nil {
				// Inside a paste only its end counts; other sequences are dropped.
				if nb, ok := next(); !ok {
					return
				} else if nb == '[' && csi(next) == "201~" {
					con.paste(pasteText(paste))
					paste = nil
				}
				continue
			}
			select {
			case nb, ok := <-ch:
				if !ok {
					return
				}
				queued.Add(-1)
				switch nb {
				case '[':
					switch csi(next) {
					case "200~":
						paste = []byte{}
					case "5~":
						con.scrollBy(1)
					case "6~":
						con.scrollBy(-1)
					}
				case 'O':
					next() // SS3: F1-F4, keypad arrows
				}
				// Alt+key and other sequences are ignored.
			case <-time.After(escWait):
				h.paneCancel()
			}
		case paste != nil:
			paste = append(paste, b)
		case b == '\r' || b == '\n':
			h.submit(con.take())
		case b == 0x7f || b == 0x08: // Backspace
			con.edit(backspace)
		case b == 0x15: // Ctrl-U
			con.edit(func(buf []rune) []rune { return buf[:0] })
		case b == 0x17: // Ctrl-W
			con.edit(deleteWord)
		case b == 0x03: // Ctrl-C clears the line, else cancels; never stops the agent
			if con.empty() {
				h.paneCancel()
			} else {
				con.edit(func(buf []rune) []rune { return buf[:0] })
			}
		case b == 0x14: // Ctrl-T
			h.paneTakeover()
		case b == '\t':
			con.edit(func(buf []rune) []rune { return append(buf, ' ') })
		case b < 0x20:
		case b < utf8.RuneSelf:
			con.edit(func(buf []rune) []rune { return append(buf, rune(b)) })
		default:
			p := []byte{b}
			for !utf8.FullRune(p) && len(p) < utf8.UTFMax {
				nb, ok := next()
				if !ok {
					return
				}
				p = append(p, nb)
			}
			if r, _ := utf8.DecodeRune(p); r != utf8.RuneError && !isPasteRune(r) {
				con.edit(func(buf []rune) []rune { return append(buf, r) })
			}
		}
	}
}

// pasteText is a pasted block as prompt text: CRLF and CR become newlines,
// tabs stay, other control bytes and invalid UTF-8 are dropped.
func pasteText(p []byte) string {
	s := strings.ReplaceAll(string(p), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Map(func(r rune) rune {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || r == utf8.RuneError || isPasteRune(r) {
			return -1
		}
		return r
	}, s)
}

func backspace(buf []rune) []rune {
	if len(buf) > 0 {
		buf = buf[:len(buf)-1]
	}
	return buf
}

// csi reads the rest of a CSI sequence (after ESC [) and returns its
// parameters and final byte.
func csi(next func() (byte, bool)) string {
	var s []byte
	for {
		b, ok := next()
		if !ok {
			return string(s)
		}
		s = append(s, b)
		if b >= 0x40 && b <= 0x7e {
			return string(s)
		}
	}
}

func deleteWord(buf []rune) []rune {
	i := len(buf)
	for i > 0 && buf[i-1] == ' ' {
		i--
	}
	for i > 0 && buf[i-1] != ' ' {
		i--
	}
	return buf[:i]
}

// submit sends a typed line: a number answers the open question, anything
// else is a prompt.
func (h *host) submit(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	h.mu.Lock()
	open := len(h.questions) > 0
	h.mu.Unlock()
	var err error
	if n, nerr := strconv.Atoi(line); open && nerr == nil {
		err = h.answer(n, "", ByPane)
	} else {
		err = h.prompt(line, ByPane)
	}
	if err != nil {
		h.say("! " + err.Error())
	}
}

// busy reports whether a turn runs or a question is open.
func (h *host) busy() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.inflight > 0 || len(h.questions) > 0 || h.state.Status == "working" || h.state.Status == "waiting"
}

// paneCancel is Esc: cancel the running turn.
func (h *host) paneCancel() {
	if !h.busy() {
		h.say("! no turn to cancel")
		return
	}
	if err := h.cancel(ByPane); err != nil {
		h.say("! cancel: " + err.Error())
	}
}

// paneTakeover is Ctrl-T: hand the session to the tool's own screen.
func (h *host) paneTakeover() {
	switch {
	case h.cfg.Takeover == nil:
		h.say("! take over is not available here")
	case h.busy():
		h.say("! take over needs an idle agent: Esc cancels the turn first")
	default:
		h.say("taking over: parking this session and resuming it in the tool's own screen…")
		if err := h.cfg.Takeover(h.say); err != nil {
			h.say("! take over: " + err.Error())
		}
	}
}

// say prints a line in the pane that is not part of the transcript.
func (h *host) say(s string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.con != nil {
		h.con.note(s)
		return
	}
	io.WriteString(h.out, s+"\n")
}
