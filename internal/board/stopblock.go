package board

import "time"

// StopReason is what fleet's Stop hook tells an agent that tries to stop
// while it owes a report (ADR-105).
const StopReason = "Call fleet_report (done, blocked or question) before stopping."

// OwesReport reports whether the agent holds a task and has not ended it:
// fleet handed it work (PromptAt) or a task card, it is not the user's
// (ADR-61), and it has no done, blocked or question report since PromptAt.
// It is ADR-68's no-report test without the idle timer, for the moment the
// tool says the turn ends.
func (a *Agent) OwesReport(now time.Time) bool {
	if a == nil || (a.PromptAt.IsZero() && a.Card == nil) || a.WithUser(now) {
		return false
	}
	r := a.Report
	return r == nil || r.At.Before(a.PromptAt) || r.State == "progress"
}

// BlockStop decides a Stop/agentStop hook: block when the agent owes a
// report, the tool says this stop is not already a blocked one's retry
// (hookActive, Claude's stop_hook_active), and fleet has not blocked a stop
// since the agent's last prompt (blockedAt, fleet's own marker, which also
// covers a tool that never sets hookActive). So at most one block per
// prompt, never two in a turn (ADR-105).
func (a *Agent) BlockStop(now time.Time, hookActive bool, blockedAt time.Time) bool {
	if hookActive || !a.OwesReport(now) {
		return false
	}
	return blockedAt.IsZero() || blockedAt.Before(a.PromptAt)
}
