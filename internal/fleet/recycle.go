package fleet

import (
	"fmt"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/spend"
)

// The recycle hint (DESIGN §7b, ADR-71). An agent whose context has been
// compacted again and again re-reads a nearly full context on every turn,
// and that is where most credits go. Once it crosses [defaults]
// recycle_compactions or recycle_aic its owner is told, once per agent, to
// hand the work to a fresh agent. Fleet does nothing to the agent itself.

// RecycleEvery is how often the overview reads every agent's spend for the
// recycle check. The spend cache makes an unchanged session file one stat.
var RecycleEvery = 30 * time.Second

// RecycleLine is the wake line and mail text for a recycle hint.
func RecycleLine(name string, sp spend.Spend) string {
	what := "has "
	switch sp.Compactions {
	case 0:
	case 1:
		what += "compacted once / "
	default:
		what += fmt.Sprintf("compacted %d times / ", sp.Compactions)
	}
	sp.Compactions = 0 // already said, not again as ⟲N
	what += "spent " + sp.Short()
	return "[fleet] " + name + " " + what + ": hand over to a fresh agent — have it write result.md, close it, respawn with result.md plus a brief."
}

// checkRecycle starts a recycle scan at most once per RecycleEvery. Called
// from Refresh by the resident overview only; the scan runs off the poll,
// since a first read of a long session file can take a while.
func (f *Fleet) checkRecycle(agents []*board.Agent, now time.Time) {
	if now.Sub(f.lastRecycle) < RecycleEvery {
		return
	}
	f.lastRecycle = now
	var cands []board.Agent
	for _, a := range agents {
		if a.RecycleTold.IsZero() && a.SessionID != "" {
			cands = append(cands, board.Agent{Name: a.Name, Profile: a.Profile, SessionID: a.SessionID})
		}
	}
	if len(cands) > 0 {
		go f.recycleScan(cands, now)
	}
}

// recycleScan wakes the owner of each agent that has newly crossed a
// recycle threshold.
func (f *Fleet) recycleScan(agents []board.Agent, now time.Time) {
	r := f.livePrefs().Recycle()
	if r.Off() {
		return
	}
	due := map[string]spend.Spend{}
	for _, a := range agents {
		sp, ok := spend.Default.Agent(f.Profiles[a.Profile], a.SessionID)
		if ok && r.Due(sp.Compactions, sp.AIC) {
			due[a.Name] = sp
		}
	}
	if len(due) == 0 {
		return
	}
	// Claim on the board first, so two processes can't both tell.
	var tell []string
	f.Store.Update(func(b *board.Board) error {
		for name := range due {
			if a, ok := b.Agents[name]; ok && a.RecycleTold.IsZero() {
				a.RecycleTold = now
				tell = append(tell, name)
			}
		}
		return nil
	})
	for _, name := range tell {
		f.reportRecycle(name, due[name])
	}
}

// reportRecycle files the hint in the owner's mailbox and wakes the owner
// through the same pending-wake path as a stuck or no-report note.
func (f *Fleet) reportRecycle(name string, sp spend.Spend) {
	log := logx.For("fleet")
	log.Info("recycle hint", "agent", name, "compactions", sp.Compactions, "aic", sp.AIC)
	line := RecycleLine(name, sp)
	up := f.ReportBox(name)
	m, err := f.Mail().ReportTo(name, up, "question", "recycle: "+line[len("[fleet] "):])
	if err != nil {
		log.Error("recycle hint failed", "agent", name, "err", err)
		return
	}
	f.wakeBox(up, m.ID, name, "question", line)
}
