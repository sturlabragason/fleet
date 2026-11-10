package fleet

import (
	"time"

	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/status"
)

// subagents updates a.Subagents from the tool's session event log (DESIGN.md
// §4 Subagents). An agent whose pane is gone or dead has nothing running.
func (f *Fleet) subagents(a *board.Agent, alive bool, now time.Time) {
	if !alive {
		a.Subagents = status.EndSubagents(a.Subagents, now)
		return
	}
	path := f.Profiles[a.Profile].SubagentLogPath(a.SessionID)
	if path == "" && a.ACP() {
		// agy keeps no event log fleet reads; its host's transcript names
		// the subagents it starts (DESIGN §7d).
		st, off := status.ReadACPSubagents(acphost.TranscriptPath(f.Home, a.Name), a.SubLogOffset, status.Subs{List: a.Subagents}, now)
		a.Subagents, a.SubLogOffset = st.List, off
		return
	}
	if path == "" {
		a.Subagents = status.PruneSubagents(a.Subagents, now)
		return
	}
	st, off := status.ReadSubagents(path, a.SubLogOffset, status.Subs{List: a.Subagents, Known: a.SubKnown}, now)
	a.Subagents, a.SubKnown, a.SubLogOffset = st.List, st.Known, off
}
