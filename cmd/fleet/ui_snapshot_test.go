package main

import (
	"os"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/wall"
	"github.com/sturlabragason/fleet/internal/watch"
)

// A poll reads the watcher's snapshot only while the watcher is live and the
// snapshot is newer than the overview's own last wall change (ADR-98).
func TestForPollUsesAFreshSnapshotOnly(t *testing.T) {
	home := t.TempDir()
	tc := &tmux.Client{Socket: "fleet-test-none", Config: "/dev/null"}
	b := &backend{f: &fleet.Fleet{Home: home, T: tc}, w: wall.New(tc, fleet.Session), enabled: true}
	snapped := func() bool { _, ok := b.ForPoll().(*pollBackend); return ok }

	now := time.Now()
	if err := watch.WriteSnapshot(home, &tmux.Snapshot{At: now}); err != nil {
		t.Fatal(err)
	}
	if snapped() {
		t.Fatal("snapshot used with no watcher")
	}
	watch.WriteBeat(home, watch.Beat{PID: os.Getpid(), Started: now, At: now, OK: now})
	if !snapped() {
		t.Fatal("fresh snapshot of a live watcher not used")
	}
	b.acted()
	if snapped() {
		t.Fatal("snapshot older than the overview's own wall change used")
	}
	watch.WriteSnapshot(home, &tmux.Snapshot{At: time.Now().Add(time.Millisecond)})
	if !snapped() {
		t.Fatal("snapshot taken after the change not used")
	}
	watch.WriteSnapshot(home, &tmux.Snapshot{At: time.Now().Add(-watch.SnapshotMaxAge - time.Second)})
	if snapped() {
		t.Fatal("stale snapshot used")
	}
}
