// Package watch keeps agent status fresh without anyone looking: one
// background process per fleet (DESIGN §7c, ADR-49). It refreshes when a
// tool's own event says something happened — a hook state file, a session
// event log — and otherwise on a slow baseline tick, so eight agents cost
// one poll a second rather than one per open window.
package watch

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
)

// Defaults for the loop. Tick is how often the cheap event check runs (a
// handful of stat calls, no processes); Interval is the longest a status may
// go unrefreshed with nothing happening.
const (
	DefaultTick     = 200 * time.Millisecond
	DefaultInterval = time.Second
	// DefaultMinGap is the floor between two event-driven refreshes. A
	// working agent appends to its session log constantly, so without it
	// every tick would be an event and the watcher would cost five capture
	// passes a second instead of the one docs/perf.md measures.
	DefaultMinGap = 500 * time.Millisecond
	// StopEvery is how often Run asks whether the fleet is still up.
	StopEvery = 5 * time.Second
)

// Watcher drives one refresh loop.
type Watcher struct {
	// Home is ~/.fleet: where the heartbeat is written.
	Home string
	// Refresh recomputes every agent's status (fleet.Fleet.Refresh).
	Refresh func() error
	// Paths lists the files an event source writes to. It is re-read after
	// every refresh, so a new agent's log is picked up without a restart.
	Paths func() []string
	// Stop ends the loop when it returns true (the fleet server is gone).
	Stop                   func() bool
	Tick, Interval, MinGap time.Duration
	Now                    func() time.Time

	seen             map[string]string
	paths            []string
	last, lastStop   time.Time
	beat, ok         time.Time
	pending          bool
	refreshes, ticks int
}

func (w *Watcher) tick() time.Duration {
	if w.Tick > 0 {
		return w.Tick
	}
	return DefaultTick
}

func (w *Watcher) interval() time.Duration {
	if w.Interval > 0 {
		return w.Interval
	}
	return DefaultInterval
}

func (w *Watcher) minGap() time.Duration {
	if w.MinGap > 0 {
		return w.MinGap
	}
	return DefaultMinGap
}

func (w *Watcher) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// Step does one pass: refresh when an event source changed or the baseline
// interval elapsed, else nothing. It reports whether it refreshed.
func (w *Watcher) Step() bool {
	now := w.now()
	w.ticks++
	why := ""
	if p := w.changed(); p != "" {
		w.pending = true
		why = "event " + p
	}
	if w.pending {
		// Events coalesce: a busy agent writes its log many times a second,
		// and one refresh covers every event since the last one. The event
		// is held, never dropped, so the refresh still happens one tick
		// after the gap rather than waiting for the baseline interval.
		if !w.last.IsZero() && now.Sub(w.last) < w.minGap() {
			return false
		}
		if why == "" {
			why = "event (deferred)"
		}
		w.pending = false
	} else if w.last.IsZero() || now.Sub(w.last) >= w.interval() {
		why = "interval"
	}
	if why == "" {
		return false
	}
	w.last = now
	w.refreshes++
	if w.Refresh != nil {
		if err := w.Refresh(); err != nil {
			logx.For("watch").Debug("refresh", "err", err, "why", why)
		} else {
			w.ok = now
		}
	} else {
		w.ok = now
	}
	// After a refresh the board is current, so this is the cheapest moment
	// to notice an agent that appeared or went away.
	w.rescan()
	return true
}

// changed returns the first watched path whose mtime or size moved since the
// last look, "" when nothing did. The first look only records what is there:
// a watcher starting up must not call every existing file an event.
func (w *Watcher) changed() string {
	if w.seen == nil {
		w.seen = map[string]string{}
		w.rescan()
		w.stampAll()
		return ""
	}
	out := ""
	for _, p := range w.paths {
		s := stamp(p)
		if w.seen[p] != s {
			w.seen[p] = s
			if out == "" {
				out = p
			}
		}
	}
	return out
}

func (w *Watcher) rescan() {
	if w.Paths == nil {
		return
	}
	w.paths = w.Paths()
	if w.seen == nil {
		return
	}
	for p := range w.seen {
		if !contains(w.paths, p) {
			delete(w.seen, p)
		}
	}
	for _, p := range w.paths {
		if _, ok := w.seen[p]; !ok {
			// A path first seen here is recorded, not treated as an event:
			// a new agent's log must not refresh on every tick until it
			// settles.
			w.seen[p] = stamp(p)
		}
	}
}

func (w *Watcher) stampAll() {
	for _, p := range w.paths {
		w.seen[p] = stamp(p)
	}
}

// stamp is a file's mtime and size, or "" when it is not there yet. A
// directory's mtime moves when a hook writes a state file into it, which is
// what makes ~/.fleet/status one cheap watch for every hook-driven agent.
func stamp(p string) string {
	fi, err := os.Stat(p)
	if err != nil {
		return ""
	}
	return fi.ModTime().UTC().Format(time.RFC3339Nano) + ":" + strconv.FormatInt(fi.Size(), 10)
}

func contains(l []string, s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}

// Run loops until ctx is done or Stop says the fleet is gone, writing the
// heartbeat other commands read to know a watcher is live.
func (w *Watcher) Run(ctx context.Context) error {
	log := logx.For("watch")
	started := w.now()
	if _, err := Claim(w.Home, started); err != nil {
		return err
	}
	defer Release(w.Home)
	log.Info("start", "tick", w.tick(), "interval", w.interval())
	t := time.NewTicker(w.tick())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("stop", "reason", "signal", "refreshes", w.refreshes, "ticks", w.ticks)
			return nil
		case <-t.C:
			w.Step()
			now := w.now()
			if now.Sub(w.beat) >= time.Second {
				w.beat = now
				if err := WriteBeat(w.Home, Beat{PID: os.Getpid(), Started: started, At: now, OK: w.ok}); err != nil {
					log.Warn("heartbeat", "err", err)
				}
			}
			if w.Stop != nil && now.Sub(w.lastStop) >= StopEvery {
				w.lastStop = now
				if w.Stop() {
					log.Info("stop", "reason", "fleet gone", "refreshes", w.refreshes, "ticks", w.ticks)
					return nil
				}
			}
		}
	}
}
