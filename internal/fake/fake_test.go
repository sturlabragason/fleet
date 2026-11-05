package fake

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sturlabragason/fleet/internal/status"
)

func TestNext(t *testing.T) {
	cases := []struct {
		s    State
		ev   Event
		r    float64
		want State
	}{
		{Working, Timer, 0.1, Waiting},
		{Working, Timer, 0.6, Error},
		{Working, Timer, 0.9, Idle},
		{Working, Input, 0.1, Working},
		{Waiting, Yes, 0, Working},
		{Waiting, No, 0, Error},
		{Waiting, Input, 0, Waiting},
		{Error, Timer, 0, Idle},
		{Idle, Timer, 0, Working},
		{Idle, Input, 0, Working},
	}
	for _, c := range cases {
		if got := Next(c.s, c.ev, c.r); got != c.want {
			t.Errorf("Next(%s,%s,%v)=%s want %s", c.s, c.ev, c.r, got, c.want)
		}
	}
}

func TestCycleVisitsAllStates(t *testing.T) {
	seen := map[State]bool{}
	s := Working
	for _, step := range []struct {
		ev Event
		r  float64
	}{{Timer, 0.1}, {Yes, 0}, {Timer, 0.6}, {Timer, 0}, {Timer, 0}} {
		seen[s] = true
		s = Next(s, step.ev, step.r)
	}
	for _, st := range []State{Working, Waiting, Error, Idle} {
		if !seen[st] {
			t.Errorf("never visited %s", st)
		}
	}
}

// The four directives exist so a screenshot or a demo can be composed. Each one
// must leave the pane classifying as the state it names, or the wall in the
// README shows the wrong story and nothing else catches it.
func TestDirectivesHoldAClassifiableState(t *testing.T) {
	set := status.Load(t.TempDir())["fake"]
	for _, c := range []struct {
		send string
		want status.Status
	}{
		{"!work add search endpoint", status.Working},
		{"!wait refactor auth middleware", status.Waiting},
		{"!error bump dependencies", status.Error},
		{"!done write migration", status.Idle},
	} {
		screen := runFake(t, "a1", c.send)
		got, ok := set.Detect(screen)
		if !ok || got != c.want {
			t.Errorf("%q classified %s (ok=%v), want %s\n--- screen ---\n%s", c.send, got, ok, c.want, screen)
		}
	}
}

// The footer is the only working marker a pane keeps once "● Task:" has
// scrolled off, so it has to carry the parentheses the pattern looks for.
func TestWorkingFooterMatchesThePattern(t *testing.T) {
	set := status.Load(t.TempDir())["fake"]
	if got, ok := set.Detect("⠹ working · 12s · (esc to interrupt)"); !ok || got != status.Working {
		t.Errorf("footer alone classified %s (ok=%v), want working", got, ok)
	}
}

// A directive turns the random timer off for good: a composed shot must not
// drift into another state while the remaining panes are being set up.
func TestDirectiveHoldsAfterTheTimerWouldHaveFired(t *testing.T) {
	screen := runFake(t, "a1", "!done write migration")
	set := status.Load(t.TempDir())["fake"]
	if got, _ := set.Detect(screen); got != status.Idle {
		t.Fatalf("held state drifted to %s", got)
	}
	if strings.Contains(screen, "!done") {
		t.Error("the directive text was echoed into the pane")
	}
}

// Step lines are sized by hand to survive a quarter-width wall pane.
func TestStepLinesFitAPane(t *testing.T) {
	for _, pool := range [][]Task{tasks, admiralTasks} {
		for _, task := range pool {
			for _, s := range task.Steps {
				if n := utf8.RuneCountInString(s); n > 50 {
					t.Errorf("step %q is %d columns, want <= 50", s, n)
				}
			}
		}
	}
}

// runFake drives the agent loop with one directive and returns everything it
// printed after the directive cleared the screen.
func runFake(t *testing.T, name, send string) string {
	t.Helper()
	in, w := io.Pipe()
	var out bytes.Buffer
	done := make(chan struct{})
	go func() { Run(name, in, &out); close(done) }()
	if _, err := io.WriteString(w, send+"\n"); err != nil {
		t.Fatal(err)
	}
	// Long enough for the directive to be read and drawn, short enough that
	// the test stays fast; the state itself is held, so there is no race on it.
	time.Sleep(300 * time.Millisecond)
	w.Close()
	<-done
	s := out.String()
	if i := strings.LastIndex(s, "\x1b[2J\x1b[H"); i >= 0 {
		s = s[i+len("\x1b[2J\x1b[H"):]
	}
	return strings.ReplaceAll(s, "\r\x1b[2K", "\n")
}
