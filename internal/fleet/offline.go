package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/netwatch"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// Network outages (DESIGN §7e, ADR-110). The resident poll probes the
// network; while it is down, fleet holds everything that would turn the
// outage into lost work (admiral wakes, the spawn queue, dead-pane reaping,
// the done sweep, recycle hints, idle parking), shows agents that failed as
// offline instead of error, and when it is back it restarts hosts that died
// and sends the agents that failed a "continue", with backoff.

var (
	// NetEvery is how often the resident poll probes while online.
	NetEvery = 30 * time.Second
	// NetOfflineEvery is how often it probes while offline.
	NetOfflineEvery = 5 * time.Second
	// NetTellAfter is how long an outage lasts before the admiral is told:
	// a blip that ends sooner is logged only, so a flapping wifi does not
	// fill the inbox.
	NetTellAfter = 20 * time.Second
	// NetRetryAfter is the first wait before a resumed agent that failed
	// again on the network gets another "continue"; each retry waits four
	// times longer, up to NetRetries.
	NetRetryAfter = 30 * time.Second
	NetRetries    = 3
	// NetStale is how long the offline file counts without a probe
	// rewriting it: a prober that stopped (process gone, probe turned off)
	// must not hold the fleet forever.
	NetStale = 2 * time.Minute
)

// ResumeText is what an agent whose turn failed in the outage is sent.
const ResumeText = "[fleet] The network was down and your last turn failed. It is back now: continue where you left off."

type netState struct {
	run   sync.Mutex // one probe at a time
	mu    sync.Mutex
	last  time.Time            // last probe started
	done  time.Time            // last probe finished
	kick  bool                 // probe on the next poll
	seen  map[string]time.Time // dead pane ID with a network error: first seen
	held  map[string]bool      // agents whose dead pane the reaper held in the outage
	back  bool                 // netBack is resuming: hold, don't record
	retry map[string]*netRetry // agents resumed after an outage
}

type netRetry struct {
	n  int       // continues sent
	at time.Time // the last one
}

type offlineFile struct {
	Since time.Time `json:"since"`
	Told  bool      `json:"told,omitempty"`
}

func (f *Fleet) offlinePath() string { return filepath.Join(f.Home, "offline") }

func (f *Fleet) readOffline() (offlineFile, bool) {
	var o offlineFile
	st, err := os.Stat(f.offlinePath())
	if err != nil || time.Since(st.ModTime()) > NetStale {
		return o, false
	}
	b, err := os.ReadFile(f.offlinePath())
	if err != nil {
		return o, false
	}
	if json.Unmarshal(b, &o) != nil || o.Since.IsZero() {
		o.Since = time.Now()
	}
	return o, true
}

func (f *Fleet) writeOffline(o offlineFile) {
	b, _ := json.Marshal(o)
	tmp := f.offlinePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err == nil {
		os.Rename(tmp, f.offlinePath())
	}
}

// Offline reports whether the resident poll found the network down, and
// since when. Every fleet process reads the same file; the prober rewrites
// it on every probe, and a file older than NetStale does not count.
func (f *Fleet) Offline() (bool, time.Time) {
	o, ok := f.readOffline()
	return ok, o.Since
}

// OfflineNote is the steady one-liner for fleet_status and the overview.
func OfflineNote(since time.Time) string {
	return "network: offline since " + since.Format("15:04") + "; agents are held and resume when it is back"
}

// netKick asks for a probe on the next poll.
func (f *Fleet) netKick() {
	f.net.mu.Lock()
	f.net.kick = true
	f.net.mu.Unlock()
}

// checkNet runs from the resident poll: a probe when due (in the
// background, one at a time) and the backoff retries of agents resumed
// after an outage.
func (f *Fleet) checkNet(as []*board.Agent, now time.Time, off bool) {
	if f.NetProbe == nil {
		return
	}
	if !off {
		f.retryNet(as, now)
	}
	every := NetEvery
	if off {
		every = NetOfflineEvery
	}
	f.net.mu.Lock()
	due := f.net.kick || now.Sub(f.net.last) >= every
	f.net.mu.Unlock()
	if !due || !f.net.run.TryLock() {
		return
	}
	f.net.mu.Lock()
	f.net.last, f.net.kick = now, false
	f.net.mu.Unlock()
	run := func() {
		defer f.net.run.Unlock()
		up := f.NetProbe()
		f.netResult(up, time.Now())
		f.net.mu.Lock()
		f.net.done = time.Now()
		f.net.mu.Unlock()
	}
	if f.NetSync {
		run()
	} else {
		go run()
	}
}

// netResult moves the shared state on one probe's answer.
func (f *Fleet) netResult(up bool, now time.Time) {
	log := logx.For("net")
	o, off := f.readOffline()
	switch {
	case !up && !off:
		log.Warn("offline")
		f.writeOffline(offlineFile{Since: now})
	case !up:
		tell := !o.Told && now.Sub(o.Since) >= NetTellAfter
		o.Told = o.Told || tell
		f.writeOffline(o) // also the prober's heartbeat
		if tell {
			for _, box := range f.AdmiralBoxes() {
				f.tellAdmiralBox(box, "network down since "+o.Since.Format("15:04:05")+": agents that fail on it show offline and are held (no crash reports, closes or new spawns). Fleet resumes them when it is back.")
			}
		}
	case up && off:
		f.netBack(o, now)
	}
}

// netBack resumes what the outage stopped, then clears the offline file:
// the resume comes first, so no poll in between shows the failed agents as
// error.
func (f *Fleet) netBack(o offlineFile, now time.Time) {
	log := logx.For("net")
	b := f.Store.Load()
	f.net.mu.Lock()
	held := f.net.held
	f.net.held, f.net.seen, f.net.back = nil, nil, true
	if f.net.retry == nil {
		f.net.retry = map[string]*netRetry{}
	}
	f.net.mu.Unlock()
	defer func() {
		f.net.mu.Lock()
		f.net.back = false
		f.net.mu.Unlock()
	}()
	by := map[string][]string{}
	for _, a := range b.Sorted() {
		var how string
		switch {
		case held[a.Name]:
			if err := f.revive(a); err != nil {
				log.Warn("restart after outage failed", "agent", a.Name, "err", err)
				continue
			}
			how = a.Name + " (restarted)"
		case a.Status == string(status.Offline):
			if err := f.Send(a.Name, ResumeText); err != nil {
				log.Warn("resume after outage failed", "agent", a.Name, "err", err)
				continue
			}
			f.net.mu.Lock()
			f.net.retry[a.Name] = &netRetry{n: 1, at: now}
			f.net.mu.Unlock()
			how = a.Name
		default:
			continue
		}
		log.Info("resumed after outage", "agent", a.Name)
		box := f.AdmiralBoxOf(a.Name)
		by[box] = append(by[box], how)
	}
	if err := os.Remove(f.offlinePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Warn("clear offline", "err", err)
	}
	took := now.Sub(o.Since).Round(time.Second)
	log.Info("online", "outage", took)
	if !o.Told {
		return
	}
	for _, box := range f.AdmiralBoxes() {
		what := "no agent needed a resume"
		if r := by[box]; len(r) > 0 {
			what = "resumed " + strings.Join(r, ", ")
		}
		f.tellAdmiralBox(box, fmt.Sprintf("network back after %s (down since %s); %s. Held reports and wakes follow; carry on.", took, o.Since.Format("15:04:05"), what))
	}
}

// revive restarts an agent whose process died in the outage: woken on its
// session when it has one, else queued again to start from its goal.
func (f *Fleet) revive(a *board.Agent) error {
	pane := f.paneOf(a.Name)
	to := string(status.Parked)
	if a.SessionID == "" {
		if !a.ACP() {
			return fmt.Errorf("no session to resume")
		}
		to = string(status.Queued)
	}
	if err := f.Store.Update(func(b *board.Board) error {
		cur, ok := b.Agents[a.Name]
		if !ok {
			return fmt.Errorf("no agent %q", a.Name)
		}
		now := time.Now()
		f.Store.AppendEvent(board.Event{Time: now, Agent: a.Name, From: cur.Status, To: to})
		cur.Status, cur.StatusSince, cur.PaneID, cur.Exit = to, now, "", nil
		cur.LastLine = "restarting after the network outage"
		if to == string(status.Queued) {
			// The dir is already its worktree.
			cur.NoWorktree = true
		}
		return nil
	}); err != nil {
		return err
	}
	if pane != "" {
		if err := f.KillPane(pane); err != nil {
			return err
		}
	}
	if to == string(status.Queued) {
		return nil // the drain starts it
	}
	_, err := f.Wake(a.Name, true)
	return err
}

// retryNet sends another "continue" to a resumed agent whose turn failed
// on the network again, after NetRetryAfter, then four times longer each
// time, NetRetries times after the first continue. An agent that comes back idle or
// waiting is done with.
func (f *Fleet) retryNet(as []*board.Agent, now time.Time) {
	f.net.mu.Lock()
	defer f.net.mu.Unlock()
	if len(f.net.retry) == 0 {
		return
	}
	on := map[string]*board.Agent{}
	for _, a := range as {
		on[a.Name] = a
	}
	for name, r := range f.net.retry {
		a := on[name]
		switch {
		case a == nil, a.Status == string(status.Idle), a.Status == string(status.Waiting), a.Status == string(status.Parked):
			delete(f.net.retry, name)
		case a.Status != string(status.Error) || !netwatch.NetError(a.LastLine):
		case r.n > NetRetries:
			logx.For("net").Warn("giving up resuming", "agent", name, "tries", r.n)
			delete(f.net.retry, name)
		case now.Sub(r.at) >= NetRetryAfter<<(2*(r.n-1)):
			r.n++
			r.at = now
			go func() {
				if err := f.Send(name, ResumeText); err != nil {
					logx.For("net").Warn("retry resume failed", "agent", name, "err", err)
				}
			}()
		}
	}
}

// holdDead reports whether the reaper must leave a dead pane alone this
// poll: always while offline (it is restarted when the network is back),
// and, online, until a probe has run since a network error was first seen
// on it, so a death that is the first sign of an outage is not reported as
// a crash.
func (f *Fleet) holdDead(a *board.Agent, p tmux.PaneInfo, off bool, now time.Time) bool {
	if f.NetProbe == nil {
		return false
	}
	f.net.mu.Lock()
	defer f.net.mu.Unlock()
	if f.net.back {
		return true // being restarted, or reaped on the next poll
	}
	if off {
		if f.net.held == nil {
			f.net.held = map[string]bool{}
		}
		f.net.held[a.Name] = true
		return true
	}
	if f.net.seen == nil {
		f.net.seen = map[string]time.Time{}
	}
	at, ok := f.net.seen[p.ID]
	if !ok {
		if !f.diedOfNet(a, p) {
			f.net.seen[p.ID] = time.Time{}
			return false
		}
		at = now
		f.net.seen[p.ID] = at
		f.net.kick = true
	}
	return !at.IsZero() && !f.net.done.After(at)
}

// diedOfNet reads the dead agent's last lines for a network error.
func (f *Fleet) diedOfNet(a *board.Agent, p tmux.PaneInfo) bool {
	if netwatch.NetError(a.LastLine) {
		return true
	}
	var screen string
	if a.ACP() {
		screen = f.acpScreen(a.Name, 20)
	} else {
		screen, _ = f.T.Capture(p.ID, 20)
	}
	for _, l := range status.Tail(screen, 20) {
		if netwatch.NetError(l) {
			return true
		}
	}
	return false
}
