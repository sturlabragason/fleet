package logo

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TestFrames: n frames of the splash's exact size, each showing more of it,
// starting near blank and ending on the static mark.
func TestFrames(t *testing.T) {
	for _, asc := range []bool{false, true} {
		fr := Frames(Splash, asc, FrameCount)
		if len(fr) != FrameCount {
			t.Fatalf("asc=%v: %d frames, want %d", asc, len(fr), FrameCount)
		}
		if fr[len(fr)-1] != Render(Splash, asc) {
			t.Errorf("asc=%v: last frame is not the static mark", asc)
		}
		full := inked(strings.Join(Plain(Splash, asc), "\n"))
		prev := -1
		for k, f := range fr {
			rows := strings.Split(ansi.Strip(f), "\n")
			if len(rows) != Splash.Height() {
				t.Fatalf("asc=%v frame %d: %d rows, want %d", asc, k, len(rows), Splash.Height())
			}
			for i, r := range rows {
				if w := ansi.StringWidth(r); w != Splash.Width() {
					t.Fatalf("asc=%v frame %d row %d: width %d, want %d", asc, k, i, w, Splash.Width())
				}
			}
			n := inked(ansi.Strip(f))
			if n < prev {
				t.Errorf("asc=%v frame %d: %d cells drawn, fewer than the frame before (%d)", asc, k, n, prev)
			}
			prev = n
		}
		if first := inked(ansi.Strip(fr[0])); first*10 > full {
			t.Errorf("asc=%v: first frame draws %d of %d cells, want under a tenth", asc, first, full)
		}
	}
	if got := Frames(Splash, false, 0); len(got) != 1 {
		t.Errorf("n=0: %d frames, want the static mark alone", len(got))
	}
}

func inked(s string) int {
	return len([]rune(strings.Join(strings.Fields(s), "")))
}

// TestSweep: cells at and past the edge are blank, the two drawn cells
// before it take the accent, the rest keep their roles.
func TestSweep(t *testing.T) {
	cells, roles := sweep([]rune("ab cdef"), []rune("ii iwww"), 5)
	if string(cells) != "ab cd  " || string(roles) != "ii aa  " {
		t.Errorf("sweep = %q %q, want %q %q", string(cells), string(roles), "ab cd  ", "ii aa  ")
	}
	cells, roles = sweep([]rune("ab cd"), []rune("ii ii"), 3)
	if string(cells) != "ab   " || string(roles) != "ia   " {
		t.Errorf("sweep at a blank = %q %q, want %q %q", string(cells), string(roles), "ab   ", "ia   ")
	}
}

// TestAnimate: the banner moves only on a person's terminal that hasn't
// asked for stillness.
func TestAnimate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		env      map[string]string
		tty      bool
		off      bool
		explicit bool
		want     bool
	}{
		{"terminal", map[string]string{"TERM": "xterm-256color"}, true, false, false, true},
		{"not a tty", nil, false, false, false, false},
		{"config off", nil, true, true, false, false},
		{"NO_COLOR", map[string]string{"NO_COLOR": "1"}, true, false, false, false},
		{"FLEET_NO_ANIM", map[string]string{"FLEET_NO_ANIM": "1"}, true, false, false, false},
		{"CI", map[string]string{"CI": "true"}, true, false, false, false},
		{"dumb", map[string]string{"TERM": "dumb"}, true, false, false, false},
		{"agent pane", map[string]string{"FLEET_AGENT": "a1"}, true, false, true, false},
		{"tmux", map[string]string{"TMUX": "/tmp/tmux-1/fleet,1,0"}, true, false, false, false},
		{"tmux, asked for", map[string]string{"TMUX": "/tmp/tmux-1/fleet,1,0"}, true, false, true, true},
		{"asked for, not a tty", nil, false, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := Gate{Getenv: func(k string) string { return tc.env[k] }, TTY: tc.tty, Off: tc.off, Explicit: tc.explicit}
			if got := Animate(g); got != tc.want {
				t.Errorf("Animate = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPlayer: ticks step through the frames and quit after the last; a key
// jumps to the last frame and quits.
func TestPlayer(t *testing.T) {
	var m tea.Model = newPlayer([]string{"a", "b", "c"})
	for _, want := range []string{"b\n", "c\n"} {
		m, _ = m.Update(tickMsg{})
		if m.View() != want {
			t.Fatalf("view %q, want %q", m.View(), want)
		}
	}
	if _, cmd := m.Update(tickMsg{}); cmd == nil || cmd() != tea.Quit() {
		t.Error("tick on the last frame does not quit")
	}
	m, cmd := newPlayer([]string{"a", "b", "c"}).Update(tea.KeyMsg{Type: tea.KeySpace})
	if m.View() != "c\n" || cmd == nil || cmd() != tea.Quit() {
		t.Errorf("key: view %q, want the last frame and quit", m.View())
	}
}

// TestBannerStatic: a terminal too narrow for the splash gets the largest
// mark that fits, printed once without a player.
func TestBannerStatic(t *testing.T) {
	var b strings.Builder
	if err := Banner(t.Context(), strings.NewReader(""), &b, 40, 10, true, true); err != nil {
		t.Fatal(err)
	}
	if got, want := ansi.Strip(b.String()), strings.Join(Plain(Mark, true), "\n")+"\n"; got != want {
		t.Errorf("narrow banner = %q, want the mark %q", got, want)
	}
}
