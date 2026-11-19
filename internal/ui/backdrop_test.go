package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/sturlabragason/fleet/internal/logo"
)

// TestBgRow: a row keeps what it shows up to its last drawn cell and the
// backdrop fills the rest, one escape per colour change.
func TestBgRow(t *testing.T) {
	m := New(&fakeBE{}, "")
	m.bg.esc = []string{"<0>", "<1>"}
	cells := []logo.FlowCell{{R: 'a'}, {R: 'b'}, {R: 'c'}, {R: 'd', Col: 1}, {R: ' ', Col: -1}, {R: 'e', Col: 1}}
	for _, tc := range []struct{ line, want string }{
		{"xy    ", "xy\x1b[0m<0>c<1>d e\x1b[0m"},
		{"", "\x1b[0m<0>abc<1>d e\x1b[0m"},
		{"xyzuvw", "xyzuvw"},
	} {
		if got := m.bgRow(tc.line, cells); got != tc.want {
			t.Errorf("bgRow(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

// TestBackdropTicks: the backdrop ticks only while it runs; still, off or
// paused it starts no timer; z pauses it across a restart; a stale tick is
// dropped.
func TestBackdropTicks(t *testing.T) {
	dir := t.TempDir()
	m := New(&fakeBE{}, dir)
	if m.bgStart() == nil {
		t.Fatal("a running backdrop starts no tick")
	}
	m.SetStill(true)
	if m.bgStart() != nil {
		t.Error("a still backdrop ticks")
	}
	m.SetStill(false)
	m.SetBackdrop(false)
	if m.bgStart() != nil {
		t.Error("a backdrop that is off ticks")
	}
	m.SetBackdrop(true)
	m.bgStart()
	stale := bgMsg{m.bg.gen - 1}
	if m.bgTick(stale) != nil {
		t.Error("a stale tick chain keeps running")
	}
	if m.key("z"); !m.bg.paused || m.bgStart() != nil {
		t.Error("z does not pause the backdrop")
	}
	if !New(&fakeBE{}, dir).bg.paused {
		t.Error("the pause is not kept across a restart")
	}
	if cmd := m.key("z"); m.bg.paused || cmd == nil {
		t.Error("z again does not start the backdrop")
	}
}

// TestBackdropLean: the strands ease toward the selected tile's row and
// never jump there in one tick.
func TestBackdropLean(t *testing.T) {
	m, _ := overview(160, 40, 6)
	now := m.now()
	m.now = func() time.Time { return now }
	m.bgStart()
	want, ok := m.selRow()
	if !ok {
		t.Fatal("the selected tile has no row")
	}
	prev := 0.0
	for range 80 {
		now = now.Add(250 * time.Millisecond)
		m.bgTick(bgMsg{m.bg.gen})
		if step := m.bg.lean.Pull - prev; step <= 0 || step > 0.1 {
			t.Fatalf("pull stepped %.3f, want a small rise", step)
		}
		prev = m.bg.lean.Pull
	}
	if m.bg.lean.Pull < 0.85 || m.bg.lean.Row < want*0.85 {
		t.Errorf("after 20 s lean = %+v, want close to row %.1f at full pull", m.bg.lean, want)
	}
}

// TestBackdropShown: a terminal without colour gets no backdrop, and the
// help is never drawn over it.
func TestBackdropShown(t *testing.T) {
	m, _ := overview(120, 30, 2)
	m.bg.key, m.bg.esc = "", nil
	lines := strings.Split(m.View(), "\n")
	if m.bg.esc == nil { // a test terminal has no colour: nothing is drawn
		for _, l := range lines {
			if strings.ContainsAny(ansi.Strip(l), "@%#") {
				t.Fatalf("backdrop drawn without colour: %q", ansi.Strip(l))
			}
		}
	}
	m.help = true
	if _, ok := m.bgShown(); ok {
		t.Error("backdrop shown behind the help")
	}
}
