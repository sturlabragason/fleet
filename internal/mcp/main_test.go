package mcp

import (
	"os"
	"testing"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/playbook"
	"github.com/sturlabragason/fleet/internal/testenv"
)

// TestMain keeps spawns hermetic: the playbook's repo-layer walk stops at the
// temp root, so no test reads the user's ~/.fleet/worker.md (ADR-62).
func TestMain(m *testing.M) {
	playbook.Ceiling = os.TempDir()
	fleet.WakeSettle = 0 // wakes typed at once in tests (ADR-93)
	os.Exit(testenv.Main(m))
}
