package acphost

import (
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
	r := startRig(t, Config{In: pr, Keys: true, Width: func() int { return 40 },
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
	r.waitFor(func() bool { return strings.HasSuffix(r.out.String(), "› par") }, "input line")
	if _, err := r.call(Request{Op: OpPrompt, Text: "from fleet"}); err != nil {
		t.Fatal(err)
	}
	r.waitFor(func() bool { return strings.Contains(r.out.String(), "> from fleet\r\n\r\x1b[2K› par") }, "redraw under output")
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

func TestConsoleClipsInputToWidth(t *testing.T) {
	var b syncBuf
	c := newConsole(&b, func() int { return 10 })
	c.edit(func([]rune) []rune { return []rune("abcdefghijklmnop") })
	if got := b.String(); !strings.HasSuffix(got, "› jklmnop") {
		t.Fatalf("line = %q", got)
	}
}
