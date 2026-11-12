package acphost

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/acp"
)

func TestKeysPromptAnswerCancelAndTakeover(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	var mu sync.Mutex
	takeovers := 0
	r := startRig(t, Config{In: pr, Keys: true, Size: func() (int, int) { return 40, 12 },
		Takeover: func(func(string)) error { mu.Lock(); takeovers++; mu.Unlock(); return nil }}, acp.AgentCapabilities{})
	type_ := func(s string) {
		t.Helper()
		if _, err := io.WriteString(pw, s); err != nil {
			t.Fatal(err)
		}
	}
	calls := func() string { return strings.Join(r.conn.Calls(), "|") }

	// Output while a line is half typed lands above it, and the line is drawn again.
	type_("par")
	r.waitFor(func() bool { return strings.Contains(lastFrame(r.out.String()), "› par") }, "input line")
	if _, err := r.call(Request{Op: OpPrompt, Text: "from fleet"}); err != nil {
		t.Fatal(err)
	}
	r.waitFor(func() bool {
		f := lastFrame(r.out.String())
		return strings.Contains(f, "› from fleet") && strings.Contains(f, "› par")
	}, "redraw under output")
	r.conn.turns <- func(handler) acp.StopReason { return acp.StopEndTurn }
	r.waitStatus("idle")

	// Ctrl-C clears a typed line and sends nothing; Backspace edits; Enter sends.
	type_("\x03do i\x7fit\r")
	r.waitStatus("working")
	ask := make(chan string, 1)
	r.conn.turns <- func(h handler) acp.StopReason {
		res, _ := h.RequestPermission(t.Context(), acp.RequestPermissionParams{
			ToolCall: acp.ToolCallRef{ToolCallID: "t1", Title: "Edit"},
			Options:  []acp.PermissionOption{{OptionID: "a", Name: "Allow"}, {OptionID: "r", Name: "Reject"}},
		})
		ask <- res.Outcome.OptionID
		return acp.StopEndTurn
	}
	r.waitStatus("waiting")
	type_("\x14") // Ctrl-T while busy is refused
	r.waitFor(func() bool { return strings.Contains(r.out.String(), "take over needs an idle agent") }, "takeover refusal")
	type_("2\r")
	if id := <-ask; id != "r" {
		t.Fatalf("typed 2 answered %q", id)
	}
	r.waitStatus("idle")
	if c := calls(); !strings.Contains(c, "prompt do it") || strings.Contains(c, "par") {
		t.Fatalf("calls = %s", c)
	}

	// An arrow key is not Esc; a lone Esc cancels the running turn.
	type_("\x1b[A")
	type_("long job\r")
	r.waitStatus("working")
	r.conn.turns <- func(handler) acp.StopReason { <-r.conn.cancel; return acp.StopCancelled }
	time.Sleep(2 * escWait)
	if strings.Contains(calls(), "cancel") {
		t.Fatalf("arrow key cancelled: %s", calls())
	}
	type_("\x1b")
	r.waitStatus("idle")
	if !strings.Contains(calls(), "cancel") {
		t.Fatalf("Esc sent no cancel: %s", calls())
	}

	// A bracketed paste keeps its newline in one prompt.
	type_("\x1b[200~line one\nline two\x1b[201~\r")
	r.waitFor(func() bool { return strings.Contains(calls(), "prompt line one\nline two") }, "pasted prompt")
	r.conn.turns <- func(handler) acp.StopReason { return acp.StopEndTurn }
	r.waitStatus("idle")

	type_("\x14")
	r.waitFor(func() bool { mu.Lock(); defer mu.Unlock(); return takeovers == 1 }, "takeover")

	// Everything typed in the pane is marked as the user's.
	by := map[string]int{}
	for _, e := range Tail(TranscriptPath(r.home, "a1"), 100) {
		if e.By == ByPane {
			by[e.Kind]++
		}
	}
	if by[KindUser] != 3 || by[KindAnswer] != 1 || by[KindCancel] != 1 {
		t.Fatalf("pane entries = %v", by)
	}
	for _, e := range r.kinds(KindUser) {
		if e.Text == "from fleet" && e.By != "" {
			t.Fatalf("fleet's prompt marked %q", e.By)
		}
	}
}

func TestConsoleWrapsInputToWidth(t *testing.T) {
	var b syncBuf
	v := newView(&b, func() (int, int) { return 10, 12 }, 0, "a1", "")
	v.paste("abcdefghijklmnop")
	v.flush()
	if got := lastFrame(b.String()); !strings.Contains(got, "› abcdefg") || !strings.Contains(got, "  hijklmn") || !strings.Contains(got, "  op") {
		t.Fatalf("frame = %q", got)
	}
}

// A large bracketed paste lands at once as one collapsed block, is drawn a
// handful of times rather than once per byte, and is sent whole on Enter.
func TestKeysLargePasteLandsCollapsed(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	r := startRig(t, Config{In: pr, Keys: true, Size: func() (int, int) { return 160, 40 }}, acp.AgentCapabilities{})
	r.waitStatus("idle")
	var text strings.Builder
	for i := range 3000 {
		fmt.Fprintf(&text, "line %04d the quick brown fox jumps over the lazy dog\r\n", i)
	}
	before := strings.Count(r.out.String(), "\x1b[?2026h")
	start := time.Now()
	go io.WriteString(pw, "\x1b[200~"+text.String()+"\x1b[201~ ok")
	r.waitFor(func() bool { return strings.Contains(lastFrame(r.out.String()), "› [Paste #1 +3000 lines] ok") }, "collapsed paste")
	if d := time.Since(start); d > time.Second {
		t.Fatalf("paste took %v", d)
	}
	if n := strings.Count(r.out.String(), "\x1b[?2026h") - before; n > 50 {
		t.Fatalf("paste drew %d frames", n)
	}
	if strings.Contains(strings.Join(r.conn.Calls(), "|"), "prompt line") {
		t.Fatalf("paste submitted before Enter: %v", r.conn.Calls())
	}
	io.WriteString(pw, "\r")
	want := "prompt " + strings.ReplaceAll(text.String(), "\r\n", "\n") + " ok"
	r.waitFor(func() bool { c := r.conn.Calls(); return len(c) > 0 && c[len(c)-1] == want }, "pasted prompt")
}

// Backspace removes a collapsed paste whole; a short one-line paste is
// typed in as it is.
func TestConsolePasteBackspace(t *testing.T) {
	var b syncBuf
	v := newView(&b, func() (int, int) { return 60, 3 }, 0, "a1", "")
	v.paste("a\nb")
	v.paste("short")
	v.redraw()
	if got := lastFrame(b.String()); !strings.Contains(got, "› [Paste #1 +2 lines]short") {
		t.Fatalf("frame = %q", got)
	}
	for range 6 {
		v.edit((*editor).backspace)
	}
	if !v.empty() {
		t.Fatalf("buf = %q", string(v.ed.buf))
	}
	v.paste("x\ny")
	if got := v.take(); got != "x\ny" {
		t.Fatalf("take = %q", got)
	}
}

// lastFrame is the last screen the view wrote.
func lastFrame(out string) string {
	if i := strings.LastIndex(out, "\x1b[?2026h"); i >= 0 {
		return out[i:]
	}
	return out
}

// Escape sequences from the terminal edit the box: arrows, Home/End,
// word jumps, Delete, Alt-Enter and Shift-Enter newlines, a mouse click,
// and Up for the last sent line.
func TestKeysEditSequences(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	r := startRig(t, Config{In: pr, Keys: true, Size: func() (int, int) { return 40, 12 }}, acp.AgentCapabilities{})
	r.waitStatus("idle")
	type_ := func(s string) {
		t.Helper()
		if _, err := io.WriteString(pw, s); err != nil {
			t.Fatal(err)
		}
	}
	sent := func(want string) {
		t.Helper()
		r.waitFor(func() bool { c := r.conn.Calls(); return len(c) > 0 && c[len(c)-1] == "prompt "+want }, want)
		r.conn.turns <- func(handler) acp.StopReason { return acp.StopEndTurn }
		r.waitStatus("idle")
	}
	// Left, Home, End, Ctrl-Left, Delete, Ctrl-A/E.
	type_("world\x1b[D\x1b[D\x1b[Hhello \x1b[F!\x1b[1;5D\x1b[3~\x01>\x05<\r")
	sent(">hello orld!<")
	// Alt-Enter, Shift-Enter (kitty and modifyOtherKeys) and Ctrl-J add lines.
	type_("a\x1b\rb\x1b[13;2uc\x1b[27;2;13~d\ne\r")
	sent("a\nb\nc\nd\ne")
	// A click on the box's first cell puts the cursor there: the box is
	// row 11 of a 12-row pane, under the rule and above the hint.
	type_("xyz")
	r.waitFor(func() bool { return strings.Contains(lastFrame(r.out.String()), "› xyz") }, "typed")
	type_("\x1b[<0;3;11M\x1b[<0;3;11m>\r")
	sent(">xyz")
	// Up recalls the last line; Ctrl-W then deletes its last word.
	type_("\x1b[A\x17again\r")
	sent("again")
}

// Enter during a turn queues the prompt; Up takes it back to edit; Esc puts
// what the pane queued back into the box instead of sending it.
func TestKeysQueueDuringTurn(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	r := startRig(t, Config{Prompt: "go", In: pr, Keys: true, Size: func() (int, int) { return 60, 16 }}, acp.AgentCapabilities{})
	r.waitStatus("working")
	type_ := func(s string) {
		t.Helper()
		if _, err := io.WriteString(pw, s); err != nil {
			t.Fatal(err)
		}
	}
	calls := func() string { return strings.Join(r.conn.Calls(), "|") }
	frame := func() string { return lastFrame(r.out.String()) }

	type_("first\r")
	r.waitFor(func() bool {
		return strings.Contains(frame(), "queued first") && strings.Contains(frame(), "Enter queue")
	}, "queued line")
	type_("\x1b[A") // Up: back into the box
	r.waitFor(func() bool {
		return !strings.Contains(frame(), "queued first") && strings.Contains(frame(), "› first")
	}, "queued prompt in the box")
	type_(" edited\r")
	r.waitFor(func() bool { return strings.Contains(frame(), "queued first edited") }, "edited prompt queued")
	type_("\x1b")
	r.waitFor(func() bool {
		return strings.Contains(calls(), "cancel") && strings.Contains(frame(), "› first edited")
	}, "Esc returns it to the box")
	r.conn.turns <- func(handler) acp.StopReason { return acp.StopCancelled }
	r.waitStatus("idle")
	if strings.Contains(calls(), "prompt first") {
		t.Fatalf("queued prompt sent after Esc: %s", calls())
	}
	type_("\r")
	r.waitFor(func() bool { return strings.Contains(calls(), "prompt first edited") }, "sent when idle")
	r.conn.turns <- func(handler) acp.StopReason { return acp.StopEndTurn }
	r.waitStatus("idle")
}
