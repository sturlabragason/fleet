package fleet

import (
	"os"
	"testing"

	"github.com/sturlabragason/fleet/internal/playbook"
	"github.com/sturlabragason/fleet/internal/testenv"
)

// TestMain keeps spawns hermetic: the playbook's repo-layer walk stops at the
// temp root, so no test reads the user's ~/.fleet/worker.md (ADR-62).
// Wakes are typed at once unless a test sets the settle window (ADR-93).
func TestMain(m *testing.M) {
	playbook.Ceiling = os.TempDir()
	WakeSettle = 0
	os.Exit(testenv.Main(m))
}
