package acphost

import (
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// The pane as a small console (ADR-82, ADR-85): the conversation above an
// input box that the user types into (editor.go, drawn by view.go). The
// terminal is in raw mode, so the host reads key presses, not lines: Enter
// sends, Esc cancels the turn, Ctrl-T hands the session to the tool's own
// screen, PgUp and PgDn scroll the conversation, and the rest edit the box.

// escWait is how long a lone Esc waits for the rest of an escape sequence
// before it counts as the Esc key.
var escWait = 50 * time.Millisecond

const (
	inputPrompt = "› "
	// Bracketed paste on and off: a pasted newline is part of the prompt,
	// not Enter.
	PasteOn  = "\x1b[?2004h"
	PasteOff = "\x1b[?2004l"
	// SGR mouse reports for button presses and the wheel: a click moves
	// the cursor in the box. tmux keeps the left drag for its selection
	// (ADR-38), so copying text still works.
	MouseOn  = "\x1b[?1000h\x1b[?1006h"
	MouseOff = "\x1b[?1006l\x1b[?1000l"
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
					if seq := csi(next); seq == "200~" {
						paste = []byte{}
					} else {
						h.csiKey(seq)
					}
				case 'O': // SS3: arrows, Home and End on some terminals
					if nb, ok := next(); ok {
						h.csiKey(string(nb))
					}
				default:
					h.altKey(nb)
				}
			case <-time.After(escWait):
				h.paneCancel()
			}
		case paste != nil:
			paste = append(paste, b)
		case b == '\r':
			h.submit(con.take())
		case b == '\n': // Ctrl-J
			con.edit(func(e *editor) { e.insert('\n') })
		case b == 0x03: // Ctrl-C clears the box, else cancels; never stops the agent
			if con.empty() {
				h.paneCancel()
			} else {
				con.edit((*editor).clear)
			}
		case b == 0x14: // Ctrl-T
			h.paneTakeover()
		case b == '\t':
			con.edit(func(e *editor) { e.insert(' ') })
		case b < 0x20 || b == 0x7f:
			if fn := ctrlKeys[b]; fn != nil {
				con.edit(fn)
			}
		case b < utf8.RuneSelf:
			con.edit(func(e *editor) { e.insert(rune(b)) })
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
				con.edit(func(e *editor) { e.insert(r) })
			}
		}
	}
}

// ctrlKeys are the editing control keys, readline style.
var ctrlKeys = map[byte]func(*editor){
	0x7f: (*editor).backspace, // Backspace
	0x08: (*editor).backspace, // Ctrl-H
	0x01: (*editor).home,      // Ctrl-A
	0x05: (*editor).end,       // Ctrl-E
	0x02: (*editor).left,      // Ctrl-B
	0x06: (*editor).right,     // Ctrl-F
	0x04: (*editor).del,       // Ctrl-D
	0x0b: (*editor).killFwd,   // Ctrl-K
	0x15: (*editor).killBack,  // Ctrl-U
	0x17: (*editor).rubout,    // Ctrl-W
	0x10: (*editor).up,        // Ctrl-P
	0x0e: (*editor).down,      // Ctrl-N
}

// altKey is ESC followed by b: Alt plus a key.
func (h *host) altKey(b byte) {
	var fn func(*editor)
	switch b {
	case '\r', '\n': // Alt-Enter
		fn = func(e *editor) { e.insert('\n') }
	case 'b':
		fn = (*editor).wordLeft
	case 'f':
		fn = (*editor).wordRight
	case 'd':
		fn = (*editor).delWordFwd
	case 0x7f, 0x08:
		fn = (*editor).delWordBack
	}
	if fn != nil {
		h.con.edit(fn)
	}
}

// csiKey acts on a CSI sequence (after ESC [) or an SS3 final byte:
// cursor keys with their xterm modifiers, Home, End, Delete, PgUp, PgDn,
// SGR mouse reports, and the modified Enter and Backspace of xterm's
// modifyOtherKeys and the kitty keyboard protocol.
func (h *host) csiKey(seq string) {
	con := h.con
	if m, ok := strings.CutPrefix(seq, "<"); ok {
		h.mouse(m)
		return
	}
	if seq == "" {
		return
	}
	final := seq[len(seq)-1]
	params := strings.Split(seq[:len(seq)-1], ";")
	num := func(i int) int {
		if i >= len(params) {
			return 0
		}
		n, _ := strconv.Atoi(params[i])
		return n
	}
	// Modifier bits: 1 Shift, 2 Alt, 4 Ctrl.
	mod := max(num(1)-1, 0)
	var fn func(*editor)
	switch final {
	case 'A':
		fn = (*editor).up
	case 'B':
		fn = (*editor).down
	case 'C':
		fn = (*editor).right
		if mod&6 != 0 {
			fn = (*editor).wordRight
		}
	case 'D':
		fn = (*editor).left
		if mod&6 != 0 {
			fn = (*editor).wordLeft
		}
	case 'H':
		fn = (*editor).home
	case 'F':
		fn = (*editor).end
	case 'u': // kitty: CSI key ; mod u
		fn = modKey(num(0), mod)
	case '~':
		switch num(0) {
		case 1, 7:
			fn = (*editor).home
		case 4, 8:
			fn = (*editor).end
		case 3:
			fn = (*editor).del
			if mod&6 != 0 {
				fn = (*editor).delWordFwd
			}
		case 5:
			con.scrollBy(1)
		case 6:
			con.scrollBy(-1)
		case 27: // modifyOtherKeys: CSI 27 ; mod ; key ~
			fn = modKey(num(2), mod)
		}
	}
	if fn != nil {
		con.edit(fn)
	}
}

// modKey is a modified key reported by its code: Enter with any modifier
// is a newline, Backspace with Ctrl or Alt deletes a word.
func modKey(code, mod int) func(*editor) {
	switch {
	case code == '\r' && mod != 0:
		return func(e *editor) { e.insert('\n') }
	case (code == 0x7f || code == 0x08) && mod&6 != 0:
		return (*editor).delWordBack
	}
	return nil
}

// mouse acts on an SGR mouse report (after ESC [ <): "b;x;yM" is a press,
// a final "m" a release. A left click in the box moves the cursor there;
// the wheel scrolls the conversation.
func (h *host) mouse(m string) {
	if m == "" || m[len(m)-1] != 'M' {
		return
	}
	f := strings.Split(m[:len(m)-1], ";")
	if len(f) != 3 {
		return
	}
	b, _ := strconv.Atoi(f[0])
	x, _ := strconv.Atoi(f[1])
	y, _ := strconv.Atoi(f[2])
	switch b {
	case 0:
		h.con.click(x, y)
	case 64:
		h.con.scrollLines(3)
	case 65:
		h.con.scrollLines(-3)
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
