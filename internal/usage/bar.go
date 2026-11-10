package usage

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/spend"
)

// MaxWidth is the most the bar may take of status-left, so the report
// badge before it is never pushed off. Past it the bar shows the total.
const MaxWidth = 90

// Levels for the bar's steady highlight (no blinking).
const (
	LevelWarn = "warn" // a known allowance or budget is 80% used
	LevelOver = "over" // past 100%
)

// WarnPct is where the highlight starts.
const WarnPct = 80

// USD formats dollars: cents below $10, whole dollars above.
func USD(v float64) string {
	if v < 10 {
		return fmt.Sprintf("$%.2f", v)
	}
	return fmt.Sprintf("$%.0f", v)
}

// CopilotPct is the share of the monthly allowance used, and the overage
// in $ past it. ok is false when no allowance is set or nothing was spent.
func (c Config) CopilotPct(monthAIC float64) (pct int, overUSD float64, ok bool) {
	if c.CopilotMonthAIC <= 0 || monthAIC <= 0 {
		return 0, 0, false
	}
	pct = int(monthAIC * 100 / c.CopilotMonthAIC)
	if over := monthAIC - c.CopilotMonthAIC; over > 0 {
		overUSD = over * USDPerAIC
	}
	return pct, overUSD, true
}

// Render is the bar text and its highlight level ("" when calm), "" text
// when nothing was spent today and no Copilot month figure exists.
//
//	today copilot $3.10 · claude-work $12 (30%) │ Copilot 38% of month
func Render(c Config, l *Ledger, now time.Time) (text, level string) {
	today := l.Today(now)
	names := make([]string, 0, len(today))
	for n := range today {
		names = append(names, n)
	}
	sort.Strings(names)
	var parts []string
	var total float64
	var totalTok int64
	unpriced := false
	for _, n := range names {
		t := today[n]
		seg := n + " " + amount(t)
		if t.Unpriced {
			unpriced = true
			totalTok += t.Tokens
		} else {
			total += t.USD
		}
		if b := c.DayUSD[n]; b > 0 && !t.Unpriced {
			pct := int(t.USD * 100 / b)
			seg += fmt.Sprintf(" (%d%%)", pct)
			level = worse(level, levelOf(pct))
		}
		parts = append(parts, seg)
	}
	var month string
	if pct, over, ok := c.CopilotPct(l.MonthAIC(now)); ok {
		month = fmt.Sprintf("Copilot %d%% of month", pct)
		if over > 0 {
			month += " +" + USD(over) + " over"
		}
		level = worse(level, levelOf(pct))
	}
	var day string
	if len(parts) > 0 {
		day = "today " + strings.Join(parts, " · ")
		if month != "" && len([]rune(day))+3+len([]rune(month)) > MaxWidth || len([]rune(day)) > MaxWidth {
			day = "today " + USD(total)
			if unpriced {
				day += " + " + spend.Tokens(totalTok) + " tok"
			}
		}
	}
	switch {
	case day != "" && month != "":
		text = day + " │ " + month
	default:
		text = day + month
	}
	return text, level
}

func amount(t Tally) string {
	if t.Unpriced {
		return spend.Tokens(t.Tokens) + " tok"
	}
	return USD(t.USD)
}

func levelOf(pct int) string {
	switch {
	case pct >= 100:
		return LevelOver
	case pct >= WarnPct:
		return LevelWarn
	}
	return ""
}

func worse(a, b string) string {
	if a == LevelOver || b == LevelOver {
		return LevelOver
	}
	if a == LevelWarn || b == LevelWarn {
		return LevelWarn
	}
	return ""
}
