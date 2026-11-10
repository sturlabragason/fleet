// Package usage turns what agents have spent into the status-line usage bar
// (DESIGN §7b, ADR-97): a day ledger that survives agent close and resets at
// local midnight, and one metric per profile that means something across
// tools. Copilot profiles show the share of the monthly AI-credit allowance
// used (and the overage in $ past 100%); every profile shows its estimated
// cost today in $ at list price. Nothing here calls a model or the network.
package usage

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sturlabragason/fleet/internal/conf"
	"github.com/sturlabragason/fleet/internal/spend"
)

// USDPerAIC is GitHub's fixed rate: 1 AI credit = $0.01.
const USDPerAIC = 0.01

// DefaultCopilotMonthAIC is Copilot Enterprise's included AI credits per
// assigned seat per month (GitHub docs, "Usage-based billing for
// organizations and enterprises", 2026). Business is 1,900, Pro 1,500,
// Pro+ 7,000, Max 20,000.
const DefaultCopilotMonthAIC = 3900

// Price is a model's list price in USD per million tokens.
type Price struct{ Input, Output, CacheRead, CacheWrite float64 }

// DefaultPrices are Anthropic's list prices by model family (5-minute
// cache writes). A model matches the longest family name it contains.
var DefaultPrices = map[string]Price{
	"opus":   {Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25},
	"sonnet": {Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
	"haiku":  {Input: 1, Output: 5, CacheRead: 0.1, CacheWrite: 1.25},
}

// Config is the [usage] section of config.toml.
type Config struct {
	// CopilotMonthAIC is the monthly AI-credit allowance the Copilot
	// percentage is of; 0 turns the percentage off.
	CopilotMonthAIC float64
	// DayUSD is an optional daily budget per profile, for a percentage on
	// a profile that has no allowance fleet knows.
	DayUSD map[string]float64
	Prices map[string]Price
}

// DefaultConfig is the config without a [usage] section.
func DefaultConfig() Config {
	ps := map[string]Price{}
	for k, v := range DefaultPrices {
		ps[k] = v
	}
	return Config{CopilotMonthAIC: DefaultCopilotMonthAIC, DayUSD: map[string]float64{}, Prices: ps}
}

// Parse reads a [usage] section over the defaults:
//
//	copilot_month_aic = 3900
//	day_usd = { claude-work = 40 }
//	price_opus = { input = 5, output = 25, cache_read = 0.5, cache_write = 6.25 }
func Parse(sec map[string]conf.Value) (Config, error) {
	c := DefaultConfig()
	if v := strings.TrimSpace(conf.Str(sec, "copilot_month_aic")); v != "" {
		n, err := num(v)
		if err != nil {
			return c, fmt.Errorf("[usage] copilot_month_aic %q: %w", v, err)
		}
		c.CopilotMonthAIC = n
	}
	for prof, v := range conf.Map(sec, "day_usd") {
		n, err := num(v)
		if err != nil {
			return c, fmt.Errorf("[usage] day_usd %s %q: %w", prof, v, err)
		}
		c.DayUSD[prof] = n
	}
	for k := range sec {
		fam, ok := strings.CutPrefix(k, "price_")
		if !ok || fam == "" {
			continue
		}
		fam = strings.ToLower(fam)
		p := c.Prices[fam]
		for field, v := range conf.Map(sec, k) {
			n, err := num(v)
			if err != nil {
				return c, fmt.Errorf("[usage] %s %s %q: %w", k, field, v, err)
			}
			switch field {
			case "input":
				p.Input = n
			case "output":
				p.Output = n
			case "cache_read":
				p.CacheRead = n
			case "cache_write":
				p.CacheWrite = n
			default:
				return c, fmt.Errorf("[usage] %s: unknown field %q", k, field)
			}
		}
		c.Prices[fam] = p
	}
	return c, nil
}

func num(v string) (float64, error) {
	n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("want a number >= 0")
	}
	return n, nil
}

// USD is sp's estimated cost at list price. Copilot reports AI credits,
// which convert at a fixed rate; a token-only tool is priced by the family
// of its last model. ok is false when no price applies.
func (c Config) USD(sp spend.Spend) (float64, bool) {
	if sp.HasAIC {
		return sp.AIC * USDPerAIC, true
	}
	p, ok := c.price(sp.Model)
	if !ok {
		return 0, false
	}
	return (float64(sp.Input)*p.Input + float64(sp.Output)*p.Output +
		float64(sp.CacheRead)*p.CacheRead + float64(sp.CacheWrite)*p.CacheWrite) / 1e6, true
}

func (c Config) price(model string) (Price, bool) {
	m := strings.ToLower(model)
	if m == "" {
		return Price{}, false
	}
	fams := make([]string, 0, len(c.Prices))
	for f := range c.Prices {
		fams = append(fams, f)
	}
	sort.Slice(fams, func(i, j int) bool {
		if len(fams[i]) != len(fams[j]) {
			return len(fams[i]) > len(fams[j])
		}
		return fams[i] < fams[j]
	})
	for _, f := range fams {
		if strings.Contains(m, f) {
			return c.Prices[f], true
		}
	}
	return Price{}, false
}
