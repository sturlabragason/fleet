package usage

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/conf"
	"github.com/sturlabragason/fleet/internal/spend"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func cop(aic float64) spend.Spend {
	return spend.Spend{Calls: 1, Input: 1000, AIC: aic, HasAIC: true}
}

func TestUSD(t *testing.T) {
	c := DefaultConfig()
	if v, ok := c.USD(cop(250)); !ok || !near(v, 2.5) {
		t.Errorf("copilot 250 AIC = %v %v, want $2.50", v, ok)
	}
	// 1M fresh in, 100k out, 2M cache read, 200k cache write on Opus.
	sp := spend.Spend{Input: 1e6, Output: 1e5, CacheRead: 2e6, CacheWrite: 2e5, Model: "claude-opus-4-5-20251101"}
	if v, ok := c.USD(sp); !ok || !near(v, 5+2.5+1+1.25) {
		t.Errorf("opus = %v %v, want 9.75", v, ok)
	}
	if _, ok := c.USD(spend.Spend{Input: 10, Model: "gemini-3"}); ok {
		t.Error("an unknown model got a price")
	}
}

func TestParse(t *testing.T) {
	d, err := conf.Parse("[usage]\ncopilot_month_aic = 1900\nprice_opus = { input = 15 }\n")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(d["usage"])
	if err != nil {
		t.Fatal(err)
	}
	if c.CopilotMonthAIC != 1900 || c.Prices["opus"].Input != 15 || c.Prices["opus"].Output != 25 {
		t.Errorf("parsed %+v", c)
	}
	d, _ = conf.Parse("[usage]\ncopilot_month_aic = lots\n")
	if _, err := Parse(d["usage"]); err == nil {
		t.Error("a bad number parsed")
	}
}

func TestCopilotPct(t *testing.T) {
	c := DefaultConfig()
	if p, over, ok := c.CopilotPct(1950); !ok || p != 50 || over != 0 {
		t.Errorf("1950 of 3900 = %d %v %v", p, over, ok)
	}
	if p, over, ok := c.CopilotPct(4400); !ok || p != 112 || !near(over, 5) {
		t.Errorf("4400 of 3900 = %d %v %v, want 112%% +$5", p, over, ok)
	}
	c.CopilotMonthAIC = 0
	if _, _, ok := c.CopilotPct(100); ok {
		t.Error("percentage without an allowance")
	}
}

func TestLedgerCountsGrowthByDay(t *testing.T) {
	c := DefaultConfig()
	l := Load(filepath.Join(t.TempDir(), "none.json"))
	day1 := time.Date(2026, 11, 10, 22, 0, 0, 0, time.Local)

	// An old session seen for the first time sets a baseline only.
	l.Observe(day1, "copilot/a", "copilot", spend.Copilot, cop(500), false, c)
	if len(l.Today(day1)) != 0 {
		t.Fatalf("first sight of an old session counted: %+v", l.Today(day1))
	}
	// A session started today counts in full.
	l.Observe(day1, "copilot/b", "copilot", spend.Copilot, cop(40), true, c)
	l.Observe(day1.Add(time.Minute), "copilot/a", "copilot", spend.Copilot, cop(560), false, c)
	if got := l.Today(day1)["copilot"]; !near(got.AIC, 100) || !near(got.USD, 1) {
		t.Errorf("today = %+v, want 100 AIC $1", got)
	}

	// After midnight the day is empty again; growth lands on the new day.
	day2 := day1.Add(3 * time.Hour)
	if len(l.Today(day2)) != 0 {
		t.Errorf("new day not empty: %+v", l.Today(day2))
	}
	l.Observe(day2, "copilot/a", "copilot", spend.Copilot, cop(600), false, c)
	if got := l.Today(day2)["copilot"]; !near(got.AIC, 40) {
		t.Errorf("day 2 = %+v, want 40 AIC", got)
	}
	if !near(l.MonthAIC(day2), 140) {
		t.Errorf("month = %v, want 140", l.MonthAIC(day2))
	}

	// A total that went down re-baselines without counting.
	l.Observe(day2, "copilot/a", "copilot", spend.Copilot, cop(10), false, c)
	l.Observe(day2, "copilot/a", "copilot", spend.Copilot, cop(30), false, c)
	if got := l.Today(day2)["copilot"]; !near(got.AIC, 60) {
		t.Errorf("after a reset = %+v, want 60 AIC", got)
	}
}

func TestLedgerSurvivesReloadAndPrunes(t *testing.T) {
	c := DefaultConfig()
	path := filepath.Join(t.TempDir(), "usage.json")
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	l := Load(path)
	l.Observe(now.Add(-50*24*time.Hour), "copilot/old", "copilot", spend.Copilot, cop(5), true, c)
	l.Observe(now, "claude/x", "claude-work", spend.Claude, spend.Spend{Output: 1e6, Model: "claude-sonnet-4-5"}, true, c)
	l.Prune(now)
	if err := l.Save(path); err != nil {
		t.Fatal(err)
	}
	l = Load(path)
	if len(l.Days) != 1 || len(l.Seen) != 1 {
		t.Errorf("after prune: %d days, %d marks, want 1 and 1", len(l.Days), len(l.Seen))
	}
	if got := l.Today(now)["claude-work"]; !near(got.USD, 15) {
		t.Errorf("claude-work = %+v, want $15", got)
	}
}

// ADR-187: --since sums whole days from the first day through today.
func TestLedgerSince(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.Local)
	l := &Ledger{Days: map[string]map[string]Tally{
		"2026-11-13": {"p": {AIC: 100}},
		"2026-11-14": {"p": {AIC: 1, Tokens: 10, USD: 0.5}},
		"2026-11-21": {"p": {AIC: 2, Unpriced: true}, "q": {Tokens: 7}},
	}}
	got := l.Since(now.AddDate(0, 0, -7))
	if p := got["p"]; !near(p.AIC, 3) || p.Tokens != 10 || !near(p.USD, 0.5) || !p.Unpriced || got["q"].Tokens != 7 || len(got) != 2 {
		t.Fatalf("since: %+v", got)
	}
}
