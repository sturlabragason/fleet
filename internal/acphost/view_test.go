package acphost

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/sturlabragason/fleet/internal/acp"
)

// screen is v's frame at w x h with the escape sequences stripped.
func screen(v *view, w, h int) []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	rows, _, _ := v.frame(w, h)
	for i, r := range rows {
		rows[i] = ansi.Strip(r)
	}
	return rows
}

func countIn(rows []string, sub string) int {
	n := 0
	for _, r := range rows {
		n += strings.Count(r, sub)
	}
	return n
}

func TestViewToolStatusUpdatesInPlace(t *testing.T) {
	v := newView(io.Discard, nil, colorprofile.TrueColor, "a1", "copilot")
	v.add(Entry{Kind: KindTool, ToolID: "t1", Title: "Build reference board HTML", Status: "pending"})
	v.add(Entry{Kind: KindTool, ToolID: "t2", Title: "Read notes.md", Status: "pending"})
	v.add(Entry{Kind: KindTool, ToolID: "t1", Status: "completed"})
	v.add(Entry{Kind: KindTool, ToolID: "t2", Status: "failed"})
	rows := screen(v, 60, 12)
	if n := countIn(rows, "Build reference board HTML"); n != 1 {
		t.Fatalf("tool drawn %d times:\n%s", n, strings.Join(rows, "\n"))
	}
	if countIn(rows, "✓ Build reference board HTML") != 1 || countIn(rows, "✗ Read notes.md failed") != 1 {
		t.Fatalf("statuses not updated in place:\n%s", strings.Join(rows, "\n"))
	}
	if countIn(rows, "pending") != 0 || countIn(rows, "○") != 1 { // the header's idle mark only
		t.Fatalf("stale status left:\n%s", strings.Join(rows, "\n"))
	}
}

func TestViewWrapsToWidthAndReflows(t *testing.T) {
	v := newView(io.Discard, nil, colorprofile.TrueColor, "a1", "")
	text := strings.Repeat("lorem ipsum dolor ", 8) + "https://example.com/" + strings.Repeat("x", 50)
	v.add(Entry{Kind: KindAgent, Text: text})
	v.add(Entry{Kind: KindUser, Text: strings.Repeat("please wrap me ", 6)})
	lines := func(w int) int {
		v.mu.Lock()
		raw, _, _ := v.frame(w, 40)
		v.mu.Unlock()
		n := 0
		for _, r := range raw {
			if got := ansi.StringWidth(r); got > w {
				t.Fatalf("width %d: row is %d wide: %q", w, got, ansi.Strip(r))
			}
			if strings.TrimSpace(ansi.Strip(r)) != "" {
				n++
			}
		}
		return n
	}
	narrow, wide := lines(30), lines(90)
	if narrow <= wide {
		t.Fatalf("no reflow: %d lines at 30 columns, %d at 90", narrow, wide)
	}
	all := strings.Join(screen(v, 30, 40), "")
	if strings.Count(all, "lorem") != 8 || !strings.Contains(strings.ReplaceAll(all, " ", ""), strings.Repeat("x", 50)) {
		t.Fatalf("text lost in wrapping:\n%s", strings.Join(screen(v, 30, 40), "\n"))
	}
}

func TestMarkdownRendering(t *testing.T) {
	ms := mdStyles{code: sty{bg: "#313244"}, link: sty{under: true}}
	md := "# Plan\n**DO-NOT list:** run `make build` and _then_ test\n- first\n2. second\n[docs](https://x.dev)\n```\n**raw** in code\n\tif x:\n```"
	var got []string
	var bold, code, under, italic []string
	for _, l := range markdown(md, 80, 2, ms) {
		var b strings.Builder
		for _, s := range l {
			b.WriteString(s.text)
			switch {
			case s.st.bold && !s.st.under:
				bold = append(bold, s.text)
			case s.st.under:
				under = append(under, s.text)
			}
			if s.st.bg != "" {
				code = append(code, s.text)
			}
			if s.st.italic {
				italic = append(italic, s.text)
			}
		}
		got = append(got, b.String())
	}
	want := []string{"  Plan", "  DO-NOT list: run make build and then test", "  • first", "  2. second", "  docs (https://x.dev)", "    **raw** in code", "        if x:"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("markdown text:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if strings.Join(bold, "|") != "DO-NOT list:" || !strings.Contains(strings.Join(code, "|"), "make build") ||
		strings.Join(italic, "|") != "then" || strings.Join(under, "|") != "Plan|docs" {
		t.Fatalf("spans: bold %q code %q italic %q underline %q", bold, code, italic, under)
	}
	// snake_case and a lone star stay text; no colour keeps the backticks.
	ms.keepTicks = true
	l := markdown("a snake_case_name * b and `x`", 80, 0, ms)
	if s := l[0][0].text + l[0][1].text; s != "a snake_case_name * b and `x`" {
		t.Fatalf("plain text changed: %q", s)
	}
}

// Text typed in the pane shows once, as the user's prompt, and the input
// line is empty after Enter: no echo of the typed line, no "> " copy.
func TestPaneShowsTypedPromptOnce(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	r := startRig(t, Config{In: pr, Keys: true, Size: func() (int, int) { return 50, 14 }}, acp.AgentCapabilities{})
	if _, err := io.WriteString(pw, "can i type here?\r"); err != nil {
		t.Fatal(err)
	}
	r.waitStatus("working")
	r.waitFor(func() bool { return strings.Contains(lastFrame(r.out.String()), "can i type here?") }, "prompt drawn")
	f := lastFrame(r.out.String())
	if n := strings.Count(f, "can i type here?"); n != 1 || !strings.Contains(f, "› can i type here?") {
		t.Fatalf("prompt drawn %d times in %q", n, f)
	}
	if strings.Contains(f, "\x1b[2J") || strings.Contains(f, "> can i") {
		t.Fatalf("frame %q", f)
	}
	r.conn.turns <- func(handler) acp.StopReason { return acp.StopEndTurn }
	r.waitStatus("idle")
}

func TestViewThoughtsFoldedAndDim(t *testing.T) {
	v := newView(io.Discard, nil, colorprofile.TrueColor, "a1", "")
	v.add(Entry{Kind: KindThought, Text: strings.Repeat("I'm thinking about the layout. ", 20)})
	v.add(Entry{Kind: KindAgent, Text: "Done."})
	rows := screen(v, 40, 20)
	n := 0
	for _, r := range rows {
		if strings.Contains(r, "thinking") || strings.Contains(r, "layout") {
			n++
		}
	}
	if n != 2 || countIn(rows, "∴") != 1 || countIn(rows, "…") != 1 {
		t.Fatalf("thought not folded to 2 lines:\n%s", strings.Join(rows, "\n"))
	}
	v.mu.Lock()
	ls := v.lines(v.blocks[0], 40)
	v.mu.Unlock()
	if s := ls[0].segs[len(ls[0].segs)-1].st; !s.italic || s.fg != v.pal.dim {
		t.Fatalf("thought style %+v", s)
	}
}

func TestColorProfileAndNoColor(t *testing.T) {
	cases := []struct {
		env  []string
		want colorprofile.Profile
	}{
		{[]string{"TERM=xterm-256color", "NO_COLOR=1"}, colorprofile.Ascii},
		{[]string{"TERM=dumb"}, colorprofile.NoTTY},
		{[]string{"TERM=tmux-256color", "TMUX=/tmp/s,1,0"}, colorprofile.TrueColor},
		{[]string{"TERM=xterm-256color"}, colorprofile.ANSI256},
	}
	for _, c := range cases {
		if got := ColorProfile(c.env); got != c.want {
			t.Errorf("%v: %v, want %v", c.env, got, c.want)
		}
	}
	bold := sty{fg: "#a6e3a1", bold: true}
	if got := (painter{colorprofile.Ascii}).paint(bold, "x"); strings.Contains(got, "38;") || !strings.Contains(got, "\x1b[1") {
		t.Errorf("NO_COLOR paint %q: want bold, no colour", got)
	}
	if got := (painter{colorprofile.NoTTY}).paint(bold, "x"); got != "x" {
		t.Errorf("dumb paint %q", got)
	}
	if got := (painter{colorprofile.TrueColor}).paint(bold, "x"); !strings.Contains(got, "38;2;166;227;161") {
		t.Errorf("true colour paint %q", got)
	}
	// No colour: the frame never blinks and nothing relies on colour alone.
	v := newView(io.Discard, nil, colorprofile.Ascii, "a1", "")
	v.add(Entry{Kind: KindStatus, Status: "working"})
	v.add(Entry{Kind: KindTool, ToolID: "t", Title: "Edit", Status: "completed"})
	v.mu.Lock()
	raw, _, _ := v.frame(40, 10)
	v.mu.Unlock()
	all := strings.Join(raw, "\n")
	if strings.Contains(all, "38;") || strings.Contains(all, "\x1b[5m") || !strings.Contains(ansi.Strip(all), "✓ Edit") || !strings.Contains(ansi.Strip(all), "working") {
		t.Fatalf("no-colour frame:\n%q", all)
	}
}

func TestViewScrollBack(t *testing.T) {
	v := newView(io.Discard, nil, colorprofile.TrueColor, "a1", "")
	for i := 0; i < 30; i++ {
		v.add(Entry{Kind: KindTool, ToolID: fmt.Sprint(i), Title: fmt.Sprintf("step %02d", i), Status: "completed"})
	}
	screen(v, 40, 10) // sets the page height
	v.scrollBy(1)
	rows := screen(v, 40, 10)
	if countIn(rows, "step ") == 0 || countIn(rows, "step 29") != 0 || countIn(rows, "newer lines") != 1 {
		t.Fatalf("scrolled frame:\n%s", strings.Join(rows, "\n"))
	}
	v.scrollBy(-5)
	if rows := screen(v, 40, 10); countIn(rows, "newer lines") != 0 || countIn(rows, "step 29") != 1 {
		t.Fatalf("back at the bottom:\n%s", strings.Join(rows, "\n"))
	}
}

func TestViewCleansControlBytes(t *testing.T) {
	v := newView(io.Discard, nil, colorprofile.TrueColor, "a1", "copilot")
	v.add(Entry{Kind: KindAgent, Text: "a\tb\x1b[2Jc\r\n- d\x07"})
	v.add(Entry{Kind: KindTool, ToolID: "t", Title: "run\x1b]0;x\x07 it"})
	v.mu.Lock()
	rows, _, _ := v.frame(40, 10)
	v.mu.Unlock()
	for _, r := range rows {
		for _, bad := range []string{"\t", "\r", "\x07", "\x1b[2J", "\x1b]"} {
			if strings.Contains(r, bad) {
				t.Fatalf("control bytes %q reached the screen: %q", bad, r)
			}
		}
	}
	all := strings.Join(screen(v, 40, 10), "\n")
	if !strings.Contains(all, "a    b[2Jc") || !strings.Contains(all, "• d") || !strings.Contains(all, "run]0;x it") {
		t.Fatalf("cleaned text:\n%s", all)
	}
}

// A resize redraws the whole pane at once, at the new size, without waiting
// for the agent to say something (a restored wall pane must not stay blank).
func TestResizeRedrawsAtOnce(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	var h atomic.Int32
	h.Store(14)
	winch := make(chan os.Signal, 1)
	r := startRig(t, Config{In: pr, Keys: true, Resized: winch, Size: func() (int, int) { return 50, int(h.Load()) }}, acp.AgentCapabilities{})
	r.waitFor(func() bool { return strings.Contains(lastFrame(r.out.String()), "\x1b[14;1H") }, "first frame")
	h.Store(9)
	winch <- syscall.SIGWINCH
	r.waitFor(func() bool {
		f := lastFrame(r.out.String())
		return strings.Contains(f, "\x1b[9;1H") && !strings.Contains(f, "\x1b[10;1H")
	}, "frame at the new size")
}
