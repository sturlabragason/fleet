package schedule

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sturlabragason/fleet/internal/burn"
)

// Defaults PROPOSED in ADR-211; the user can change them there.
const (
	QuietFrom  = 22 // local hour a timer run is skipped from
	QuietTo    = 7  // local hour quiet ends
	MaxRunning = 2  // scheduled agents open at once
)

// Gate is what decides whether one firing starts its agent.
type Gate struct {
	Now      time.Time
	Due      time.Time     // the calendar time the timer fired for
	Interval time.Duration // gap between firings
	Timer    bool          // fired by the timer; a manual run only checks overlap
	LastOpen string        // the route's previous agent, if still on the board
	Running  int           // scheduled agents on the board
	QuotaLow string        // why the profile's quota is low, "" if fine or unknown
}

// Check is "" when the run may start, else why it is skipped.
func (g Gate) Check() string {
	if g.LastOpen != "" {
		return "overlap: the last run's agent " + g.LastOpen + " is still open"
	}
	if !g.Timer {
		return ""
	}
	if Quiet(g.Now) {
		return fmt.Sprintf("quiet hours (%02d:00-%02d:00)", QuietFrom, QuietTo)
	}
	if !g.Due.IsZero() && g.Interval > 0 && g.Now.Sub(g.Due) > g.Interval/2 {
		return fmt.Sprintf("missed: catch-up came %s late, more than half the interval", g.Now.Sub(g.Due).Round(time.Minute))
	}
	if g.Running >= MaxRunning {
		return fmt.Sprintf("%d scheduled agents already open (cap %d)", g.Running, MaxRunning)
	}
	if g.QuotaLow != "" {
		return "quota low: " + g.QuotaLow
	}
	return ""
}

// Quiet reports whether t falls in quiet hours, local time.
func Quiet(t time.Time) bool {
	h := t.Hour()
	return h >= QuietFrom || h < QuietTo
}

// burnTail is how much of burn.jsonl QuotaLow reads: the last tick fits.
const burnTail = 256 << 10

// QuotaLow reads the last fleet burn tick (ADR-190) and says why profile's
// quota is under reserve percent, "" when it is fine, not tracked, or the
// tick is older than two hours.
func QuotaLow(home, profile string, reserve float64, now time.Time) string {
	f, err := os.Open(filepath.Join(home, "logs", "burn.jsonl"))
	if err != nil {
		return "" // no burn log: quota not tracked
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > burnTail {
		if _, err := f.Seek(-burnTail, io.SeekEnd); err != nil {
			return ""
		}
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	b = bytes.TrimRight(b, "\n")
	var tick burn.TickResult
	if json.Unmarshal(b[bytes.LastIndexByte(b, '\n')+1:], &tick) != nil || now.Sub(tick.At) > 2*time.Hour {
		return ""
	}
	for _, p := range tick.Profiles {
		if p.Profile != profile || !p.Quota.Available {
			continue
		}
		for _, w := range p.Quota.Windows {
			if w.RemainingPct < reserve {
				return fmt.Sprintf("%s %s has %.0f%% left (reserve %.0f%%)", profile, w.Kind, w.RemainingPct, reserve)
			}
		}
	}
	return ""
}
