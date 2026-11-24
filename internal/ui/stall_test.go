package ui

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLoopWatch(t *testing.T) {
	steps := []struct {
		name  string
		beat  time.Duration // stored before the check; 0 leaves the beat alone
		now   time.Duration
		want  string
		dumps int // dump calls so far
	}{
		{"no beat yet", 0, time.Minute, "", 0},
		{"recent beat", time.Second, 5 * time.Second, "", 0},
		{"idle just under", 0, time.Second + stallAfter - time.Millisecond, "", 0},
		{"idle 10s", 0, time.Second + stallAfter, "stalled", 1},
		{"still idle", 0, time.Hour, "", 1},
		{"beat again", time.Hour + time.Second, time.Hour + 2*time.Second, "recovered", 1},
		{"healthy", 0, time.Hour + 3*time.Second, "", 1},
		{"stalls again", 0, time.Hour + time.Second + stallAfter, "stalled", 2},
	}
	var w loopWatch
	dumps := 0
	dump := func() string { dumps++; return "goroutine 1 [running]:" }
	for _, s := range steps {
		if s.beat != 0 {
			w.beat.Store(int64(s.beat))
		}
		if got := w.check(s.now, dump); got != s.want || dumps != s.dumps {
			t.Fatalf("%s: check = %q with %d dumps, want %q with %d", s.name, got, dumps, s.want, s.dumps)
		}
	}
}

func TestStacksHasThisGoroutine(t *testing.T) {
	if s := stacks(); !strings.Contains(s, "TestStacksHasThisGoroutine") {
		t.Fatalf("stacks lacks the caller:\n%.500s", s)
	}
}

// blockedTTY is a terminal tmux has stopped reading: a write waits for release.
type blockedTTY struct {
	once             sync.Once
	entered, release chan struct{}
}

func (b *blockedTTY) Write(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return len(p), nil
}

// TestBlockedFrameStallsPatch: while Bubble Tea's frame write is stuck on the
// terminal, a backdrop patch (run from View on the event loop) waits for it.
// That is how a pane whose output tmux stops draining freezes the overview.
func TestBlockedFrameStallsPatch(t *testing.T) {
	tty := &blockedTTY{entered: make(chan struct{}), release: make(chan struct{})}
	out := &SyncOut{w: tty}
	go func() { _, _ = out.Write([]byte("frame")) }()
	<-tty.entered
	patched := make(chan struct{})
	go func() { out.patch([]byte("cells")); close(patched) }()
	select {
	case <-patched:
		t.Fatal("patch returned while the frame write was blocked")
	case <-time.After(50 * time.Millisecond):
	}
	close(tty.release)
	select {
	case <-patched:
	case <-time.After(5 * time.Second):
		t.Fatal("patch still blocked after the terminal drained")
	}
}
