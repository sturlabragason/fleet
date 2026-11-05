package status

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEventState(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		event string
		want  Status
		ok    bool
	}{
		{"Stop", Idle, true},
		{"stop", Idle, true},
		{"Notification", Waiting, true},
		{"pre-tool-use", Working, true},
		{"UserPromptSubmit", Working, true},
		{"SubagentStop", Working, true},
		{"CompletelyMadeUp", "", false},
		{"", "", false},
	} {
		s, ok := EventState(c.event, "", now)
		if ok != c.ok || s.Status != c.want {
			t.Errorf("%q: got %q,%v want %q,%v", c.event, s.Status, ok, c.want, c.ok)
		}
	}
}

func TestStateRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "status")
	now := time.Now().Round(time.Millisecond)
	if _, ok := ReadState(dir, "a1"); ok {
		t.Fatal("state before any hook ran")
	}
	s, _ := EventState("notification", "Claude needs your permission to use Bash", now)
	if err := WriteState(dir, "a1", s); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadState(dir, "a1")
	if !ok || got.Status != Waiting || got.WaitingFor != s.WaitingFor || !got.At.Equal(now) {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
	// A half-written or broken file must not decide an agent's status.
	os.WriteFile(filepath.Join(dir, "a1.json"), []byte("{not json"), 0o644)
	if _, ok := ReadState(dir, "a1"); ok {
		t.Error("broken state file was trusted")
	}
	WriteState(dir, "a1", s)
	DropState(dir, "a1")
	if _, ok := ReadState(dir, "a1"); ok {
		t.Error("state survived DropState")
	}
}

// Writing a state bumps the directory's mtime: that is the one path the
// watcher has to stat to notice any agent's hook (ADR-49).
func TestWriteStateTouchesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "status")
	s, _ := EventState("stop", "", time.Now())
	if err := WriteState(dir, "a1", s); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := WriteState(dir, "a2", s); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(dir)
	if !after.ModTime().After(before.ModTime()) {
		t.Errorf("dir mtime did not move: %v -> %v", before.ModTime(), after.ModTime())
	}
}
