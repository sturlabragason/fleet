package fleet

import "github.com/sturlabragason/fleet/internal/status"

// UpgradeQuiet reports whether a new fleet binary may be swapped in now
// (ADR-114): no agent on the board is working and every admiral's pane
// reads idle. The swap itself interrupts nothing (each process re-execs at
// its own safe point), but a turn is left to finish on the build it began.
func (f *Fleet) UpgradeQuiet() bool {
	for _, a := range f.Store.Load().Agents {
		if a.Status == string(status.Working) {
			return false
		}
	}
	if f.T == nil {
		return true
	}
	for _, name := range f.AdmiralNames() {
		if v := f.admiralView(name); v.Pane != "" && !v.Idle {
			return false
		}
	}
	return true
}
