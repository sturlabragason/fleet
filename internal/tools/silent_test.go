package tools

import (
	"os/exec"
	"testing"
)

// tmux shows a failing run-shell -b command's exit status in view mode over
// a pane (the wall), so a Silent command prints nothing and exits 0.
func TestSilentHidesOutputAndExitStatus(t *testing.T) {
	for _, sh := range []string{"true", "false", "sleep 0; sh -c 'echo out; echo err >&2; exit 3'"} {
		out, err := exec.CommandContext(t.Context(), "sh", "-c", Silent(sh)).CombinedOutput()
		if err != nil || len(out) != 0 {
			t.Errorf("%q: err %v, output %q", sh, err, out)
		}
	}
}
