package acphost

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// TestBackdropOverlay: when enabled with colour, blank spans of the
// conversation body carry blurred low-opacity glass glyphs while transcript
// text and the input row stay intact; without colour nothing is drawn.
func TestBackdropOverlay(t *testing.T) {
	var buf bytes.Buffer
	v := newView(&buf, func() (int, int) { return 60, 14 }, colorprofile.TrueColor, "a1", "copilot")
	v.bordered = true
	v.focus = -1 // unfocused: still frame, no timer started
	v.enableBackdrop(false, 42)
	v.add(Entry{Kind: KindAgent, Text: "hello world"})
	v.mu.Lock()
	v.paint(true)
	hasTimer := v.bg.timer != nil
	v.mu.Unlock()
	if hasTimer {
		t.Error("unfocused ACP pane started a backdrop timer")
	}
	out := ansi.Strip(buf.String())
	if !strings.Contains(out, "hello world") {
		t.Fatalf("transcript text missing from output: %q", out)
	}
	if !strings.ContainsAny(out, "░·") {
		t.Errorf("blurred glass backdrop missing from coloured ACP pane: %q", out)
	}
	v.close()

	// Without colour (NO_COLOR / Ascii), no backdrop glyphs appear.
	var plain bytes.Buffer
	vp := newView(&plain, func() (int, int) { return 60, 14 }, colorprofile.Ascii, "a1", "copilot")
	vp.bordered = true
	vp.enableBackdrop(true, 1)
	vp.add(Entry{Kind: KindAgent, Text: "plain text"})
	vp.mu.Lock()
	vp.paint(true)
	esc := vp.bg.esc
	vp.mu.Unlock()
	if esc != nil || strings.ContainsRune(ansi.Strip(plain.String()), '░') {
		t.Errorf("backdrop drawn on colourless ACP pane (esc=%v): %q", esc != nil, ansi.Strip(plain.String()))
	}
	vp.close()
}

// TestBackdropFocusTimer: focusing an animated ACP pane starts the backdrop
// timer, unfocusing or closing stops it cleanly.
func TestBackdropFocusTimer(t *testing.T) {
	var buf bytes.Buffer
	v := newView(&buf, func() (int, int) { return 60, 14 }, colorprofile.TrueColor, "a1", "copilot")
	v.bordered = true
	v.focus = -1
	v.enableBackdrop(false, 7)
	v.setFocus(true)
	v.mu.Lock()
	started := v.bg.timer != nil
	v.mu.Unlock()
	if !started {
		t.Fatal("focusing an animated ACP pane did not start the backdrop timer")
	}
	v.setFocus(false)
	v.mu.Lock()
	stopped := v.bg.timer == nil
	v.mu.Unlock()
	if !stopped {
		t.Error("unfocusing an ACP pane left the backdrop timer running")
	}
	v.setFocus(true)
	v.close()
	v.mu.Lock()
	closedStop := v.bg.timer == nil
	v.mu.Unlock()
	if !closedStop {
		t.Error("closing an ACP pane left the backdrop timer running")
	}
}
