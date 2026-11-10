package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The overview follows a rebuilt bin/fleet (ADR-62): a replaced binary that
// fails the probe keeps the old code and is not retried; one that passes
// quits the TUI and asks cmdUI to exec it. An unchanged binary does nothing.
func TestUIFollowsRebuiltBinary(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "fleet")
	os.WriteFile(exe, []byte("v1"), 0o755)
	probes, quits := 0, 0
	var fail error
	r := newUIReexec(exe, exe, func(string) error { probes++; return fail }, func() { quits++ })
	replace := func(body string) {
		os.WriteFile(exe+".new", []byte(body), 0o755)
		os.Rename(exe+".new", exe)
	}
	r.check()
	if probes != 0 || quits != 0 || r.wanted() {
		t.Fatalf("unchanged binary: probes %d quits %d", probes, quits)
	}
	replace("v2")
	fail = errors.New("half-written")
	r.check()
	r.check()
	if probes != 1 || quits != 0 || r.wanted() {
		t.Fatalf("failed probe: probes %d quits %d wanted %v", probes, quits, r.wanted())
	}
	replace("v3")
	fail = nil
	r.check()
	if probes != 2 || quits != 1 || !r.wanted() {
		t.Fatalf("good build: probes %d quits %d wanted %v", probes, quits, r.wanted())
	}
	r.check()
	if quits != 1 {
		t.Fatalf("quit again after the hand-over was arranged: %d", quits)
	}
}

// A quit while the probe runs stays a quit: the probe that finishes after
// the TUI ended does not exec. run stops after a hand-over and on cancel.
func TestUIReexecHaltAndRun(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "fleet")
	os.WriteFile(exe, []byte("v1"), 0o755)
	var r *uiReexec
	r = newUIReexec(exe, exe, func(string) error {
		if r.halt() {
			t.Error("halt during the probe reported a hand-over")
		}
		return nil
	}, func() { t.Error("quit after the TUI had ended") })
	os.WriteFile(exe+".new", []byte("v2"), 0o755)
	os.Rename(exe+".new", exe)
	r.check()
	if r.wanted() {
		t.Fatal("hand-over arranged after the TUI ended")
	}

	quits := 0
	r2 := newUIReexec(exe, exe, func(string) error { return nil }, func() { quits++ })
	done := make(chan struct{})
	go func() { r2.run(context.Background(), 5*time.Millisecond); close(done) }()
	os.WriteFile(exe+".new", []byte("v3"), 0o755)
	os.Rename(exe+".new", exe)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not stop after a hand-over")
	}
	if quits != 1 || !r2.halt() {
		t.Fatalf("hand-over: quits %d wanted %v", quits, r2.wanted())
	}

	ctx, cancel := context.WithCancel(context.Background())
	r3 := newUIReexec(exe, exe, func(string) error { return nil }, func() {})
	done = make(chan struct{})
	go func() { r3.run(ctx, 5*time.Millisecond); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not stop on cancel")
	}
}
