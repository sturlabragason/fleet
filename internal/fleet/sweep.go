package fleet

import (
	"sync"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/status"
)

// doneGrace is how long after its done report an agent may still be
// finishing its turn; an agent that went idle later than this did more work
// after reporting and is left alone by the idle sweep.
const doneGrace = 5 * time.Minute

// sweepEvery limits how often one agent's git state is re-checked.
const sweepEvery = time.Minute

type doneSweep struct {
	run     sync.Mutex
	mu      sync.Mutex
	checked map[string]time.Time
}

// sweepDone closes agents that reported done, then sat idle for their
// profile's auto_close_idle, once AgentBlocker passes (DESIGN §6.7). It
// catches work merged after the report and closes that failed at report
// time. Git runs in the background; one sweep at a time.
func (f *Fleet) sweepDone(as []*board.Agent, now time.Time) {
	var due []*board.Agent
	f.sweep.mu.Lock()
	if f.sweep.checked == nil {
		f.sweep.checked = map[string]time.Time{}
	}
	for _, a := range as {
		pr := f.Prefs.For(a.Profile)
		if !pr.AutoCloseDone || pr.AutoCloseIdle <= 0 || !idleDone(a, pr.AutoCloseIdle, now) {
			continue
		}
		if now.Sub(f.sweep.checked[a.Name]) < sweepEvery {
			continue
		}
		f.sweep.checked[a.Name] = now
		due = append(due, a)
	}
	f.sweep.mu.Unlock()
	if len(due) == 0 || !f.sweep.run.TryLock() {
		return
	}
	go func() {
		defer f.sweep.run.Unlock()
		log := logx.For("fleet")
		for _, a := range due {
			r := f.userBlocker(a)
			if r == "" {
				r = AgentBlocker(a)
			}
			if r != "" {
				log.Debug("idle close skipped", "agent", a.Name, "reason", r)
				continue
			}
			if err := f.closeLater(a.Name); err != nil {
				log.Warn("idle close failed", "agent", a.Name, "err", err)
				continue
			}
			log.Info("auto-closing idle done agent", "agent", a.Name)
		}
	}()
}

// idleDone: a done report, idle for at least after, and the idle spell began
// no later than doneGrace after the report.
func idleDone(a *board.Agent, after time.Duration, now time.Time) bool {
	r := a.Report
	if r == nil || r.State != "done" || a.Status != string(status.Idle) {
		return false
	}
	if a.StatusSince.After(r.At.Add(doneGrace)) {
		return false
	}
	since := a.StatusSince
	if r.At.After(since) {
		since = r.At
	}
	return now.Sub(since) >= after
}
