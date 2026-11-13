package sysx

import (
	"os"
	"strings"
)

// OwnerEnv tags every process an agent's pane starts with the agent that
// owns it, as NAME@FLEET_HOME (ADR-122). The tag survives reparenting, so
// fleet can find an agent's processes after they left its pane's tree.
const OwnerEnv = "FLEET_OWNER"

// DropOwner returns env without OwnerEnv; nil env means this process's.
// Daemons fleet starts (the watcher, monitors, the tmux server) must not
// carry an agent's tag, or they would be reaped with it.
func DropOwner(env []string) []string {
	if env == nil {
		if _, ok := os.LookupEnv(OwnerEnv); !ok {
			return nil
		}
		env = os.Environ()
	}
	out := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, OwnerEnv+"=") {
			out = append(out, e)
		}
	}
	return out
}
