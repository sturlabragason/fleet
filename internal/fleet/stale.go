package fleet

import (
	"os"
	"sort"
	"strings"
)

// StaleHosts names the ACP agents whose host still runs a fleet binary other
// than the one at f.Exe: the part of a release that does not apply live
// (ADR-111). A host holds the agent's process and its input box, so it moves
// to the new build only when the agent is parked and woken.
func (f *Fleet) StaleHosts() []string {
	want, err := os.Stat(f.Exe)
	if err != nil {
		return nil
	}
	var out []string
	for name, a := range f.Store.Load().Agents {
		if !a.ACP() || a.PaneID == "" {
			continue
		}
		pid, err := f.T.Run("display-message", "-p", "-t", a.PaneID, "#{pane_pid}")
		if err != nil || strings.TrimSpace(pid) == "" {
			continue
		}
		got, err := os.Stat("/proc/" + strings.TrimSpace(pid) + "/exe")
		if err != nil || (os.SameFile(got, want) && got.ModTime().Equal(want.ModTime())) {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
