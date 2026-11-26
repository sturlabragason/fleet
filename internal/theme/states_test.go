package theme

import (
	"testing"

	"github.com/sturlabragason/fleet/internal/status"
)

// Every status fleet has draws from the table, not the unknown fallback, and
// every role in the table is a status role the palette fills.
func TestStateTableCoversStatuses(t *testing.T) {
	for _, st := range []status.Status{status.Working, status.Waiting, status.Error, status.Stuck, status.Idle,
		status.Unknown, status.Gone, status.Queued, status.Starting, status.Parked, status.Offline} {
		if _, ok := states[string(st)]; !ok {
			t.Errorf("status %q has no row in the state table", st)
		}
	}
	roles := map[string]bool{"working": true, "waiting": true, "error": true, "stuck": true, "idle": true, "gone": true}
	for name, s := range states {
		if !roles[s.Role] || s.Glyph == "" {
			t.Errorf("state %q: role %q glyph %q", name, s.Role, s.Glyph)
		}
	}
	for name, s := range reports {
		if !roles[s.Role] {
			t.Errorf("report %q: role %q", name, s.Role)
		}
	}
}

func TestStateLookups(t *testing.T) {
	p := lane
	tests := []struct {
		st, glyph, hex string
		rank           int
	}{
		{"waiting", "●", p.Waiting, 0},
		{"starting", "▸", p.Working, 3},
		{"offline", "⊘", p.Idle, 6},
		{"queued", "‖", p.Gone, 7},
		{"nonsense", "?", p.Idle, 6},
	}
	for _, tc := range tests {
		if g, h, r := Glyph(tc.st), p.Status(tc.st), Rank(tc.st); g != tc.glyph || h != tc.hex || r != tc.rank {
			t.Errorf("%s: got %q %s %d, want %q %s %d", tc.st, g, h, r, tc.glyph, tc.hex, tc.rank)
		}
	}
}
