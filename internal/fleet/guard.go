package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/park"
	"github.com/sturlabragason/fleet/internal/prefs"
)

// The memory guard (DESIGN §7, ADR-44). When free RAM falls under
// [memory] min_free, fleet parks the longest-idle agent -- never a pinned,
// working, waiting or prompted one -- and holds new spawns in the queue
// instead of starting them. Everything it does is reversible: `fleet wake`
// brings a parked agent back, and `guard = false` switches it off.

// FreeMem reads free memory; nil = /proc/meminfo (tests replace it).
func (f *Fleet) freeMem() (uint64, error) {
	if f.FreeMem != nil {
		return f.FreeMem()
	}
	return park.Free()
}

// LowMemory reports whether the guard is on and free memory is under
// min_free, with the reading and the settings behind that answer.
func (f *Fleet) LowMemory() (bool, uint64, prefs.Memory) {
	m := f.MemPrefs()
	if !m.Guard || m.MinFree == 0 {
		return false, 0, m
	}
	free, err := f.freeMem()
	if err != nil {
		logx.For("fleet").Debug("memory guard: no reading", "err", err)
		return false, 0, m
	}
	return free < m.MinFree, free, m
}

// LowMemoryNote is the one-line reason a spawn was queued by the guard.
func LowMemoryNote(free uint64, m prefs.Memory) string {
	return fmt.Sprintf("memory guard: %s free, under min_free %s", park.Bytes(free), park.Bytes(m.MinFree))
}

// guardMemory parks one agent per check when memory is low: the
// longest-idle one, and tells the admiral. One agent at a time, so the
// memory it frees is seen before the next is taken.
// The reading comes from the caller, so the drain and the guard act on
// one view of memory per poll.
func (f *Fleet) guardMemory(as []*board.Agent, now time.Time, low bool, free uint64, m prefs.Memory) {
	if !low {
		f.parkIdle(as, now, m)
		return
	}
	c := park.Candidates(as, f.pinned())
	if len(c) == 0 {
		logx.For("fleet").Debug("memory guard: nothing to park", "free", park.Bytes(free), "min_free", park.Bytes(m.MinFree))
		return
	}
	// Claim only once there is something to park, so an interval is never
	// spent on a check that does nothing.
	claimed, before := f.claimGuard(now, m.Every)
	if !claimed {
		return
	}
	a := c[0]
	idle := now.Sub(a.StatusSince).Round(time.Second)
	reason := LowMemoryNote(free, m)
	if err := f.Park(a.Name, reason); err != nil {
		// Give the interval back: nothing was parked, and under real
		// pressure the next poll must be free to try another agent.
		f.releaseGuard(before)
		logx.For("fleet").Warn("memory guard: park failed", "agent", a.Name, "err", err)
		return
	}
	logx.For("fleet").Info("memory guard: parked", "agent", a.Name, "idle", idle, "free", park.Bytes(free), "min_free", park.Bytes(m.MinFree))
	f.tellAdmiralBox(f.AdmiralBoxOf(a.Name), fmt.Sprintf("parked %s (idle %s) to free memory: %s. Wake it with fleet_park name=%s action=wake when there is room again.",
		a.Name, idle, reason, a.Name))
}

// parkIdle parks the longest-idle agent once it has been idle for
// [memory] park_idle (ADR-106), one per check_every like the low-memory
// path. It only logs: the board shows the agent parked, and a report per
// idle park would wake the admiral for nothing.
func (f *Fleet) parkIdle(as []*board.Agent, now time.Time, m prefs.Memory) {
	if !m.Guard || m.ParkIdle <= 0 {
		return
	}
	c := park.Candidates(as, f.pinned())
	if len(c) == 0 || now.Sub(c[0].StatusSince) < m.ParkIdle {
		return
	}
	claimed, before := f.claimGuard(now, m.Every)
	if !claimed {
		return
	}
	a := c[0]
	idle := now.Sub(a.StatusSince).Round(time.Second)
	if err := f.Park(a.Name, fmt.Sprintf("idle %s, over park_idle %s", idle, m.ParkIdle)); err != nil {
		f.releaseGuard(before)
		logx.For("fleet").Warn("idle park failed", "agent", a.Name, "err", err)
		return
	}
	logx.For("fleet").Info("idle park", "agent", a.Name, "idle", idle, "park_idle", m.ParkIdle)
}

// guardName is the sender of the guard's reports to the admiral.
const guardName = "fleet"

// tellAdmiral posts a report from fleet itself to the admiral's inbox and
// nudges the pane, so an auto-park is never silent (DESIGN §6.7).
func (f *Fleet) tellAdmiral(text string) { f.tellAdmiralBox(mail.Admiral, text) }

// tellAdmiralBox is tellAdmiral to one admiral's mailbox (DESIGN §6.12).
func (f *Fleet) tellAdmiralBox(box, text string) {
	m, err := f.Mail().ReportTo(guardName, box, "progress", text)
	if err != nil {
		logx.For("fleet").Warn("memory guard: report failed", "err", err)
		return
	}
	f.wakeBox(box, m.ID, guardName, "progress", ReportLineTo(box, guardName, "progress", mail.FoldSummary(text)))
}

// claimGuard rate-limits the guard to one park per check_every across every
// process that drains (the overview, the MCP server, `fleet ls`), using the
// mtime of one file as the shared stamp. The read and the write happen
// under a flock on that file, so two processes polling together can't both
// see the old stamp and both park.
// It returns the stamp it replaced, so a claim that ends up parking
// nothing can hand the interval back with releaseGuard.
func (f *Fleet) claimGuard(now time.Time, every time.Duration) (bool, time.Time) {
	if err := os.MkdirAll(f.Home, 0o755); err != nil {
		return false, time.Time{}
	}
	p := filepath.Join(f.Home, "memguard")
	lf, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return false, time.Time{}
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return false, time.Time{}
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	var before time.Time
	// A fresh file (size 0) has never been claimed; its mtime is now.
	if st, err := lf.Stat(); err == nil && st.Size() > 0 {
		if now.Sub(st.ModTime()) < every {
			return false, time.Time{}
		}
		before = st.ModTime()
	}
	if _, err := lf.WriteAt([]byte(now.Format(time.RFC3339)+"\n"), 0); err != nil {
		return false, time.Time{}
	}
	if err := os.Chtimes(p, now, now); err != nil {
		return false, time.Time{}
	}
	return true, before
}

// releaseGuard puts the stamp back to what it was before a claim that
// parked nothing.
func (f *Fleet) releaseGuard(before time.Time) {
	p := filepath.Join(f.Home, "memguard")
	if before.IsZero() {
		os.Remove(p)
		return
	}
	os.Chtimes(p, before, before)
}
