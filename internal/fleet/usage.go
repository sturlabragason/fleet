package fleet

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/spend"
	"github.com/sturlabragason/fleet/internal/usage"
)

// The usage bar (DESIGN §7b, ADR-97): the badge writer (the watcher while
// it is live, else the overview, ADR-75) reads every agent's spend at most
// once per RecycleEvery, adds the growth to the day ledger and sets two
// global tmux options that `fleet up`'s status-left shows. Reads go through
// the shared spend cache, so an unchanged session file costs one stat.
const (
	UsageOption      = "@fleet_usage"
	UsageLevelOption = "@fleet_usage_level"
)

var usageTick struct {
	sync.Mutex
	last time.Time
	busy bool
}

// UsagePath is the day ledger.
func (f *Fleet) UsagePath() string { return filepath.Join(f.Home, "usage.json") }

// UsageConfig is the live [usage] config; a bad section logs and falls
// back to the defaults.
func (f *Fleet) UsageConfig() usage.Config {
	c, err := usage.Parse(f.livePrefs().UsageSection())
	if err != nil {
		logx.For("usage").Warn("config.toml; using the defaults", "err", err)
		return usage.DefaultConfig()
	}
	return c
}

// syncUsage starts a usage pass off the poll, at most once per
// RecycleEvery and never two at once.
func (f *Fleet) syncUsage(now time.Time) {
	usageTick.Lock()
	if usageTick.busy || now.Sub(usageTick.last) < RecycleEvery {
		usageTick.Unlock()
		return
	}
	usageTick.busy, usageTick.last = true, now
	usageTick.Unlock()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logx.For("usage").Error("usage pass panicked", "panic", r)
			}
			usageTick.Lock()
			usageTick.busy = false
			usageTick.Unlock()
		}()
		f.SyncUsage(now)
	}()
}

// SyncUsage updates the ledger and sets the bar options, touching tmux
// only when the text or its level changed.
func (f *Fleet) SyncUsage(now time.Time) {
	c := f.UsageConfig()
	l := f.ObserveUsage(now, c)
	text, level := usage.Render(c, l, now)
	changed := f.setOpt(UsageOption, text)
	if f.setOpt(UsageLevelOption, level) || changed {
		f.T.Run("refresh-client", "-S")
	}
}

// ObserveUsage reads the spend of every agent on the board and of those
// closed today, adds what grew to the ledger, saves it and returns it.
func (f *Fleet) ObserveUsage(now time.Time, c usage.Config) *usage.Ledger {
	path := f.UsagePath()
	l := usage.Load(path)
	b := f.Store.Load()
	today := usage.Day(now)
	see := func(a *board.Agent) {
		key, kind := f.usageKey(a)
		if key == "" {
			return
		}
		sp, ok := f.SpendOf(a)
		if !ok {
			return
		}
		l.Observe(now, key, a.Profile, kind, sp, usage.Day(a.Created.In(now.Location())) == today, c)
	}
	for _, a := range b.Agents {
		see(a)
	}
	for _, cl := range b.ClosedRecords() {
		if usage.Day(cl.At.In(now.Location())) == today {
			a := cl.Agent
			see(&a)
		}
	}
	l.Prune(now)
	if err := l.Save(path); err != nil {
		logx.For("usage").Warn("save ledger", "err", err)
	}
	return l
}

// usageKey names an agent's session in the ledger, "" when it has none
// fleet can read.
func (f *Fleet) usageKey(a *board.Agent) (string, spend.Kind) {
	if _, k := spend.Source(f.Profiles[a.Profile], a.SessionID); k != "" {
		return a.Profile + "/" + a.SessionID, k
	}
	if a.ACP() {
		return a.Profile + "/acp/" + a.Name, spend.ACP
	}
	return "", ""
}
