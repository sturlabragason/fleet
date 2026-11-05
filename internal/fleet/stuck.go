package fleet

import (
	"strings"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/prefs"
)

// The stuck rule's reporting half (DESIGN §4, ADR-45). The rule itself is in
// internal/status; Refresh claims the start of an episode on the board, which
// is what makes the admiral hear about it exactly once, and hands it here.

// stuckWakeLine caps the screen line quoted in a stuck wake line.
const stuckWakeLine = 80

type stuckNote struct {
	name, profile, pane, reason, line string
}

// reportStuck files one stuck episode in the admiral's mailbox, wakes the
// admiral and, only when the agent's profile asks for it, interrupts the
// pane. Nothing here pauses, parks or kills the agent.
func (f *Fleet) reportStuck(n stuckNote) {
	log := logx.For("fleet")
	log.Info("stuck", "agent", n.name, "reason", n.reason)
	text := "stuck: " + n.reason
	act := f.Prefs.For(n.profile).StuckAction
	if act == prefs.StuckInterrupt {
		text += "\ninterrupting: Escape sent to the pane (stuck_action = interrupt)"
	}
	if n.line != "" && !strings.Contains(n.reason, n.line) {
		text += "\n" + n.line
	}
	up := f.ReportsTo(n.name)
	if m, err := f.Mail().ReportTo(n.name, up, "blocked", text); err != nil {
		log.Error("stuck report failed", "agent", n.name, "err", err)
	} else {
		f.wakeBox(boxOf(up), m.ID, n.name, "blocked", StuckLine(n.name, n.reason, act))
	}
	if act == prefs.StuckInterrupt {
		f.interruptStuck(n)
	}
}

// interruptStuck sends one Escape to a stuck agent's pane, under the same
// guards as a report wake (ADR-36): the pane must still be alive and still
// be that agent's (a recycled %id would otherwise take the key), and the
// user must not be typing, so an Escape can't drop a half-written line.
func (f *Fleet) interruptStuck(n stuckNote) {
	log := logx.For("fleet")
	if n.pane == "" {
		return
	}
	panes, err := f.T.ListPanes("")
	if err != nil {
		log.Warn("stuck interrupt skipped", "agent", n.name, "err", err)
		return
	}
	ok := false
	for _, p := range panes {
		if p.ID == n.pane && p.Agent == n.name && !p.Dead {
			ok = true
			break
		}
	}
	if !ok {
		log.Warn("stuck interrupt skipped: pane is not the agent's any more", "agent", n.name, "pane", n.pane)
		return
	}
	if !f.quiet() {
		log.Info("stuck interrupt skipped: the user is typing", "agent", n.name)
		return
	}
	if err := f.pressKeys(n.pane, "Escape"); err != nil {
		log.Warn("stuck interrupt failed", "agent", n.name, "err", err)
	}
}

// StuckLine is the line typed into an idle admiral pane for a stuck episode.
func StuckLine(name, reason, act string) string {
	if r := []rune(reason); len(r) > stuckWakeLine {
		reason = string(r[:stuckWakeLine-1]) + "…"
	}
	tail := " Not paused; look with fleet_peek."
	if act == prefs.StuckInterrupt {
		tail = " Escape sent (stuck_action = interrupt)."
	}
	return "[fleet] " + name + " looks stuck: " + strings.TrimRight(reason, ".") + "." + tail + " Full text: call fleet_reports."
}
