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

// An event inside the coalescing gap is held, not dropped: the refresh
// happens as soon as the gap is over, not at the next baseline interval.
func TestEventInGapIsDeferred(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "events.jsonl")
	os.WriteFile(log, []byte("start\n"), 0o644)
	n := 0
	now := time.Now()
	w := &Watcher{Home: t.TempDir(), Refresh: func() error { n++; return nil },
		Paths: func() []string { return []string{log} }, Interval: time.Hour,
		MinGap: 500 * time.Millisecond, Now: func() time.Time { return now }}
	w.Step()
	was := n
	// One event, then quiet: it lands inside the gap and must still be
	// refreshed about half a second later.
	os.WriteFile(log, []byte("start\nline\n"), 0o644)
	now = now.Add(100 * time.Millisecond)
	w.Step()
	if n != was {
		t.Fatal("the gap did not hold the event")
	}
	now = now.Add(500 * time.Millisecond)
	w.Step()
	if n != was+1 {
		t.Errorf("deferred event never refreshed (%d refreshes)", n-was)
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

// Claim is an flock on watch.lock held for the watcher's life (ADR-62): the
// kernel drops it when the process dies, so a pid written in the file is
// never the test. A lock file naming a live pid that holds no flock (a crash
// left it, and the pid was recycled) is free; an older fleet's watcher, which
// holds no flock either, still counts while its heartbeat is fresh.
func TestClaim(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	if _, err := Claim(home, now); err != nil {
		t.Fatal(err)
	}
	if !Live(home, now) {
		t.Error("claiming did not make a live heartbeat")
	}
	if pid, ok := Running(home); !ok || pid != os.Getpid() {
		t.Errorf("a claimed home has no running watcher: %d %v", pid, ok)
	}
	Release(home)
	if _, ok := ReadBeat(home); ok {
		t.Error("release left a heartbeat")
	}
	cases := []struct {
		name  string
		lock  string
		beat  *Beat
		claim bool
	}{
		{"older watcher with a fresh beat", "1\n", &Beat{PID: 1, Started: now, At: now, OK: now}, false},
		{"pid file with no flock and no beat", "1\n", nil, true},
		{"pid file with no flock and a stale beat", "1\n", &Beat{PID: 1, Started: now.Add(-time.Hour), At: now.Add(-time.Minute), OK: now.Add(-time.Minute)}, true},
		{"dead holder", "2147483647\n", &Beat{PID: 0x7FFFFFFF, Started: now, At: now, OK: now}, true},
		{"junk lock", "\n", nil, true},
	}
	for _, c := range cases {
		os.WriteFile(filepath.Join(home, "watch.lock"), []byte(c.lock), 0o644)
		os.Remove(BeatPath(home))
		if c.beat != nil {
			WriteBeat(home, *c.beat)
		}
		_, err := Claim(home, now)
		if c.claim && err != nil {
			t.Errorf("%s: claim refused: %v", c.name, err)
		}
		if !c.claim && err == nil {
			t.Errorf("%s: claimed a home someone else holds", c.name)
		}
		Release(home)
	}
	// Clear never takes a live older watcher's heartbeat.
	WriteBeat(home, Beat{PID: 1, Started: now, At: now, OK: now})
	Clear(home)
	if _, ok := ReadBeat(home); !ok {
		t.Error("Clear removed a live watcher's claim")
	}
}

// Two claims on one home cannot both hold, even from one process (flock is
// per open file), and the second gets the holder's pid; after Release the
// home is free again. This is what stops duplicate watchers.
func TestClaimIsExclusive(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	if _, err := Claim(home, now); err != nil {
		t.Fatal(err)
	}
	// The holder's heartbeat is irrelevant: a new watcher holds the flock
	// before its first beat.
	os.Remove(BeatPath(home))
	if _, err := Claim(home, now); err == nil {
		t.Fatal("a second claim succeeded while the first is held")
	}
	if pid, ok := Running(home); !ok || pid != os.Getpid() {
		t.Fatalf("Running = %d, %v", pid, ok)
	}
	Clear(home)
	if _, err := os.Stat(filepath.Join(home, "watch.lock")); err != nil {
		t.Fatal("Clear removed a held lock file")
	}
	Release(home)
	if _, err := Claim(home, now); err != nil {
		t.Fatalf("claim after release: %v", err)
	}
	Release(home)
}

// The watcher re-execs itself when its binary is replaced (ADR-62, like the
// MCP reload of ADR-25), so a resident watcher never runs code older than
// bin/fleet; a failed re-exec keeps the old code and is not retried until the
// file changes again; with no Reexec it stops instead.
func TestBinaryChangeReexecs(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "fleet")
	os.WriteFile(exe, []byte("v1"), 0o755)
	calls := 0
	var fail error
	w := &Watcher{Home: t.TempDir(), Exe: exe, Reexec: func() error { calls++; return fail }}
	w.selfExe = exe
	now := time.Now()
	if w.lifecycle(now) || calls != 0 {
		t.Fatalf("unchanged binary: stop or reexec (%d)", calls)
	}
	replace := func(body string) {
		tmp := exe + ".new"
		os.WriteFile(tmp, []byte(body), 0o755)
		os.Rename(tmp, exe)
	}
	replace("v2")
	fail = os.ErrPermission
	if w.lifecycle(now.Add(StopEvery)) || calls != 1 {
		t.Fatalf("changed binary: calls=%d", calls)
	}
	if w.lifecycle(now.Add(2*StopEvery)) || calls != 1 {
		t.Fatalf("a failed reexec was retried on the same file: calls=%d", calls)
	}
	replace("v3")
	fail = nil
	w.lifecycle(now.Add(3 * StopEvery))
	if calls != 2 {
		t.Fatalf("new build not re-execed: calls=%d", calls)
	}
	w2 := &Watcher{Home: t.TempDir(), Exe: exe}
	w2.selfExe = exe
	w2.lifecycle(now)
	replace("v4")
	if !w2.lifecycle(now.Add(StopEvery)) {
		t.Fatal("no Reexec: a changed binary must stop the watcher")
	}
}

// A watcher whose home was deleted stops (ADR-62).
func TestHomeGoneStops(t *testing.T) {
	home := filepath.Join(t.TempDir(), "h")
	os.MkdirAll(home, 0o755)
	w := &Watcher{Home: home}
	if w.lifecycle(time.Now()) {
		t.Fatal("stopped with its home present")
	}
	os.RemoveAll(home)
	if !w.lifecycle(time.Now().Add(StopEvery)) {
		t.Fatal("kept running with its home gone")
	}
}

// StaleExe tells `fleet up` that a running watcher runs another binary than
// the one starting the fleet, deleted or replaced, so it is restarted.
func TestStaleExe(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if StaleExe(os.Getpid(), self) {
		t.Error("own binary called stale")
	}
	other := filepath.Join(t.TempDir(), "fleet")
	os.WriteFile(other, []byte("x"), 0o755)
	if !StaleExe(os.Getpid(), other) {
		t.Error("another binary not called stale")
	}
}

// A refresh that panics is logged and the loop goes on; the heartbeat's OK
// stays at the last good refresh, so the panic shows as a stale watcher
// (ADR-113).
func TestRefreshPanicKeepsLooping(t *testing.T) {
	w, _ := newWatcher(t, t.TempDir())
	now := time.Now()
	w.Now = func() time.Time { return now }
	w.Interval = time.Second
	w.Step()
	good := w.ok
	w.Refresh = func() error { var m map[string]int; m["x"] = 1; return nil }
	now = now.Add(2 * time.Second)
	if !w.Step() {
		t.Fatal("panicking pass did not count as a refresh")
	}
	if !w.ok.Equal(good) {
		t.Errorf("OK moved to %v after a panic", w.ok)
	}
	n := 0
	w.Refresh = func() error { n++; return nil }
	now = now.Add(2 * time.Second)
	if !w.Step() || n != 1 || !w.ok.Equal(now) {
		t.Fatalf("loop did not recover: n=%d ok=%v", n, w.ok)
	}
}
