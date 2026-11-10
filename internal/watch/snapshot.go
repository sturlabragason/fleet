package watch

import (
	"os"
	"path/filepath"
	"time"

	"github.com/sturlabragason/fleet/internal/tmux"
)

// SnapshotMaxAge is how old a shared tmux snapshot may be and still be used.
// The watcher writes one per refresh, at least once a second; an older one
// means it stalled, and the reader goes back to asking tmux itself.
const SnapshotMaxAge = 3 * time.Second

// SnapshotPath is the watcher's shared tmux snapshot under home (ADR-94).
func SnapshotPath(home string) string { return filepath.Join(home, "tmux.json") }

// WriteSnapshot shares s for the overview.
func WriteSnapshot(home string, s *tmux.Snapshot) error {
	return tmux.WriteSnapshot(SnapshotPath(home), s)
}

// ReadSnapshot returns the shared snapshot when it was taken after since and
// is at most SnapshotMaxAge old at now.
func ReadSnapshot(home string, since, now time.Time) (*tmux.Snapshot, bool) {
	s, err := tmux.ReadSnapshot(SnapshotPath(home))
	if err != nil || !s.At.After(since) || now.Sub(s.At) > SnapshotMaxAge {
		return nil, false
	}
	return s, true
}

// DropSnapshot removes the shared snapshot when the watcher stops.
func DropSnapshot(home string) { _ = os.Remove(SnapshotPath(home)) }
