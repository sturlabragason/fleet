package fleet

import (
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
)

// The re-surface (ADR-130). An agent's question or blocked report that has
// waited on the user longer than [reports] resurface_after wakes its owner
// once more, once per report: the claim is board.Agent.ResurfacedAt, so it
// survives restarts like the stuck and recycle notes (ADR-45, ADR-71).

// ResurfaceLine is the wake line, and after "[fleet] " the note's text.
func ResurfaceLine(name string, waited time.Duration, head string) string {
	if r := []rune(head); len(r) > wakeHeadline {
		head = string(r[:wakeHeadline-1]) + "…"
	}
	return "[fleet] " + name + " has waited " + board.Age(waited) + " on the user: " + head
}

// resurface wakes the owner of each open ask past resurface_after. Called
// from Refresh by the housekeeping process only.
func (f *Fleet) resurface(agents []*board.Agent, now time.Time) {
	after := f.livePrefs().ResurfaceAfter()
	if after <= 0 {
		return
	}
	due := func(a *board.Agent) bool {
		return a.OpenAsk() && now.Sub(a.Report.At) >= after && !a.ResurfacedAt.Equal(a.Report.At)
	}
	found := false
	for _, a := range agents {
		found = found || due(a)
	}
	if !found {
		return
	}
	// Claim on the board first, so two processes can't both tell.
	var tell []board.Agent
	if err := f.Store.Update(func(b *board.Board) error {
		for _, a := range b.Agents {
			if due(a) {
				a.ResurfacedAt = a.Report.At
				tell = append(tell, board.Agent{Name: a.Name, Report: &board.Report{State: a.Report.State, Summary: a.Report.Summary, At: a.Report.At}})
			}
		}
		return nil
	}); err != nil {
		logx.For("fleet").Warn("re-surface claim failed; telling at the next poll", "err", err)
		return
	}
	log := logx.For("fleet")
	for _, a := range tell {
		line := ResurfaceLine(a.Name, now.Sub(a.Report.At), a.Report.Summary)
		log.Info("ask re-surfaced", "agent", a.Name, "waited", now.Sub(a.Report.At).Round(time.Minute))
		up := f.ReportBox(a.Name)
		m, err := f.Mail().ReportTo(a.Name, up, "question", "resurface: "+line[len("[fleet] "):])
		if err != nil {
			log.Error("re-surface note failed", "agent", a.Name, "err", err)
			continue
		}
		f.wakeBox(up, m.ID, a.Name, "question", line)
	}
}
