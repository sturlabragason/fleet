package web

import (
	"os"
	"testing"

	"github.com/sturlabragason/fleet/internal/testenv"
)

// TestMain fails the run if a test leaves a tmux server or process behind.
func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }
