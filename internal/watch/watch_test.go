package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newWatcher returns a watcher over paths with a counter instead of a
// refresh and no gap between event refreshes, so one write is one refresh.
func newWatcher(t *testing.T, paths ...string) (*Watcher, *int) {
	t.Helper()
	n := 0
	w := &Watcher{Home: t.TempDir(), Refresh: func() error { n++; return nil },
		Paths: func() []string { return paths }, Interval: time.Hour, MinGap: time.Nanosecond}
	return w, &n
}

// The first pass records what is on disk and refreshes once (the baseline);
// after that only a changed file wakes it.
func TestStepRefreshesOnEvent(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "a1.json")
	w, n := newWatcher(t, dir, state)
	if !w.Step() || *n != 1 {
		t.Fatalf("first step: refreshed=%v n=%d", *n > 0, *n)
	}
	if w.Step() || *n != 1 {
		t.Fatalf("quiet step refreshed: n=%d", *n)
	}
	if err := os.WriteFile(state, []byte(`{"status":"waiting"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !w.Step() || *n != 2 {
		t.Fatalf("hook write did not wake the watcher: n=%d", *n)
	}
	if w.Step() || *n != 2 {
		t.Fatalf("the same event fired twice: n=%d", *n)
	}
	// A second write to the same file is a new event.
	os.WriteFile(state, []byte(`{"status":"idle"}`), 0o644)
	if !w.Step() || *n != 3 {
		t.Fatalf("second hook write missed: n=%d", *n)
	}
}

// With nothing happening the baseline interval still refreshes, and no more
// often than that.
func TestStepInterval(t *testing.T) {
	w, n := newWatcher(t, t.TempDir())
	now := time.Now()
	w.Now = func() time.Time { return now }
	w.Interval = time.Second
	w.Step()
	for i := 0; i < 4; i++ {
		now = now.Add(200 * time.Millisecond)
		w.Step()
	}
	if *n != 1 {
		t.Fatalf("refreshed %d times inside one interval", *n)
	}
	now = now.Add(200 * time.Millisecond)
	if !w.Step() || *n != 2 {
		t.Fatalf("interval did not refresh: n=%d", *n)
	}
}

// A new agent's log appearing in Paths is recorded, not replayed as an event
// on every tick.
func TestNewPathIsNotAStorm(t *testing.T) {
	dir := t.TempDir()
	paths := []string{dir}
	n := 0
	w := &Watcher{Home: t.TempDir(), Refresh: func() error { n++; return nil },
		Paths: func() []string { return paths }, Interval: time.Hour, MinGap: time.Nanosecond}
	w.Step()
	extra := filepath.Join(dir, "new.jsonl")
	os.WriteFile(extra, []byte("x\n"), 0o644)
	paths = append(paths, extra)
	w.Step() // the directory changed: one refresh, which also rescans
	before := n
	if w.Step() || n != before {
		t.Fatalf("the newly watched file kept firing: %d -> %d", before, n)
	}
}

// A busy agent's log is written many times a second; the watcher must
// coalesce those into one refresh per MinGap, or it costs several capture
// passes a second (docs/perf.md).
func TestEventsCoalesce(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "events.jsonl")
	os.WriteFile(log, []byte("start\n"), 0o644)
	n := 0
	now := time.Now()
	w := &Watcher{Home: t.TempDir(), Refresh: func() error { n++; return nil },
		Paths: func() []string { return []string{log} }, Interval: time.Hour,
		MinGap: 500 * time.Millisecond, Now: func() time.Time { return now }}
	w.Step()
	for i := 0; i < 10; i++ {
		now = now.Add(100 * time.Millisecond)
		f, _ := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o644)
		f.WriteString("line\n")
		f.Close()
		w.Step()
	}
	if n > 3 {
		t.Errorf("%d refreshes for 1 s of a chatty log", n)
	}
	if n < 2 {
		t.Errorf("%d refreshes: events stopped waking the watcher", n)
	}
}

func TestBeatLive(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	if Live(home, now) {
		t.Error("live with no heartbeat")
	}
	if err := WriteBeat(home, Beat{PID: os.Getpid(), Started: now, At: now, OK: now}); err != nil {
		t.Fatal(err)
	}
	if !Live(home, now) {
		t.Error("own pid with a fresh beat should be live")
	}
	if Live(home, now.Add(StaleAfter+time.Second)) {
		t.Error("stale beat counted as live")
	}
	// A watcher that breathes but whose refreshes keep failing is not live:
	// whoever asked must refresh for itself rather than read a frozen board.
	WriteBeat(home, Beat{PID: os.Getpid(), Started: now, At: now, OK: now.Add(-time.Minute)})
	if Live(home, now) {
		t.Error("a watcher with no successful refresh counted as live")
	}
	DropBeat(home)
	if _, ok := ReadBeat(home); ok {
		t.Error("beat survived DropBeat")
	}
	// A heartbeat from a pid that is gone is not live, and is not ours to
	// delete. Pid 0x7FFFFFFF is above every pid_max in practice.
	WriteBeat(home, Beat{PID: 0x7FFFFFFF, Started: now, At: now, OK: now})
	if Live(home, now) {
		t.Error("dead pid counted as live")
	}
	DropBeat(home)
	if _, ok := ReadBeat(home); !ok {
		t.Error("dropped another process's heartbeat")
	}
}

// Claim is the token two fleets racing to start a watcher fight over: the
// second one loses while the first is live, and takes over when it is not.
func TestClaim(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	if _, err := Claim(home, now); err != nil {
		t.Fatal(err)
	}
	if !Live(home, now) {
		t.Error("claiming did not make a live heartbeat")
	}
	// Another process holding a live claim blocks us (pid 1 always exists).
	WriteBeat(home, Beat{PID: 1, Started: now, At: now, OK: now})
	if _, err := Claim(home, now); err == nil {
		t.Error("claimed a home another live watcher holds")
	}
	// A dead holder does not.
	WriteBeat(home, Beat{PID: 0x7FFFFFFF, Started: now, At: now, OK: now})
	if _, err := Claim(home, now); err != nil {
		t.Errorf("stale claim not taken over: %v", err)
	}
	Release(home)
	if _, ok := ReadBeat(home); ok {
		t.Error("release left a heartbeat")
	}
	if _, err := Claim(home, now); err != nil {
		t.Errorf("claim after release: %v", err)
	}
}
