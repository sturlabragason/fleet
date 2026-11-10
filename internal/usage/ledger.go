package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/sturlabragason/fleet/internal/spend"
)

// Keep is how long the ledger keeps day totals and session marks: past a
// month, so the Copilot month figure always has its whole month.
const Keep = 40 * 24 * time.Hour

// Mark is a session's running total when fleet last read it.
type Mark struct {
	AIC    float64   `json:"aic,omitempty"`
	Tokens int64     `json:"tokens,omitempty"`
	USD    float64   `json:"usd,omitempty"`
	At     time.Time `json:"at"`
}

// Tally is one profile's spend on one day.
type Tally struct {
	Kind   spend.Kind `json:"kind,omitempty"`
	AIC    float64    `json:"aic,omitempty"`
	Tokens int64      `json:"tokens,omitempty"`
	USD    float64    `json:"usd,omitempty"`
	// Unpriced is set when part of the day's tokens had no price, so the
	// $ figure is short and the bar shows tokens instead.
	Unpriced bool `json:"unpriced,omitempty"`
}

// Ledger is ~/.fleet/usage.json. A session's spend is counted on the day
// fleet sees it grow: each read adds the growth since the session's last
// mark to today's tally, keyed by local date, so closed agents still count
// and the day starts empty at midnight.
type Ledger struct {
	Seen map[string]Mark             `json:"seen"`
	Days map[string]map[string]Tally `json:"days"`
}

// Day is the ledger key for t's local date.
func Day(t time.Time) string { return t.Format("2006-01-02") }

// Load reads the ledger; a missing or unreadable file is an empty ledger.
func Load(path string) *Ledger {
	l := &Ledger{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, l)
	}
	if l.Seen == nil {
		l.Seen = map[string]Mark{}
	}
	if l.Days == nil {
		l.Days = map[string]map[string]Tally{}
	}
	return l
}

// Save writes the ledger atomically.
func (l *Ledger) Save(path string) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Observe records one session's running total sp. key names the session
// (profile and session id). fresh says the session started today, so a
// first sight counts all of it; otherwise a first sight only sets the
// baseline, since fleet cannot tell which day the earlier spend was on.
// A total that went down (a replaced file) re-baselines without counting.
func (l *Ledger) Observe(now time.Time, key, profile string, kind spend.Kind, sp spend.Spend, fresh bool, c Config) {
	usd, priced := c.USD(sp)
	cur := Mark{AIC: sp.AIC, Tokens: sp.Total(), USD: usd, At: now}
	prev, seen := l.Seen[key]
	l.Seen[key] = cur
	if !seen {
		if !fresh {
			return
		}
		prev = Mark{}
	}
	d := Mark{AIC: cur.AIC - prev.AIC, Tokens: cur.Tokens - prev.Tokens, USD: cur.USD - prev.USD}
	if d.AIC < 0 || d.Tokens < 0 || d.USD < 0 {
		return
	}
	if d.AIC == 0 && d.Tokens == 0 {
		return
	}
	day := Day(now)
	if l.Days[day] == nil {
		l.Days[day] = map[string]Tally{}
	}
	t := l.Days[day][profile]
	t.Kind = kind
	t.AIC += d.AIC
	t.Tokens += d.Tokens
	t.USD += d.USD
	if !priced && d.Tokens > 0 {
		t.Unpriced = true
	}
	l.Days[day][profile] = t
}

// Prune drops days and marks older than Keep.
func (l *Ledger) Prune(now time.Time) {
	cut := now.Add(-Keep)
	for k, m := range l.Seen {
		if m.At.Before(cut) {
			delete(l.Seen, k)
		}
	}
	for d := range l.Days {
		if t, err := time.ParseInLocation("2006-01-02", d, now.Location()); err != nil || t.Before(cut) {
			delete(l.Days, d)
		}
	}
}

// Today is today's tallies by profile.
func (l *Ledger) Today(now time.Time) map[string]Tally { return l.Days[Day(now)] }

// MonthAIC is the AI credits Copilot profiles spent this calendar month.
// GitHub resets the allowance at 00:00 UTC on the 1st; days are keyed by
// local date, so the edge is off by the UTC offset at most.
func (l *Ledger) MonthAIC(now time.Time) float64 {
	month := now.Format("2006-01")
	var sum float64
	for d, ts := range l.Days {
		if len(d) < 7 || d[:7] != month {
			continue
		}
		for _, t := range ts {
			if t.Kind == spend.Copilot {
				sum += t.AIC
			}
		}
	}
	return sum
}
