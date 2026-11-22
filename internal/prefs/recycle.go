package prefs

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sturlabragason/fleet/internal/conf"
)

// Recycle is when an agent's owner is told to hand its work to a fresh
// agent (DESIGN §7b, ADR-71): after this many context compactions or this
// many AI credits. 0 turns a threshold off. [defaults] only.
type Recycle struct {
	Compactions int     // [defaults] recycle_compactions
	AIC         float64 // [defaults] recycle_aic
}

// DefaultRecycle: one compaction or 500 AIC (ADR-195). Agents that compacted
// were 18% of fleet sessions but 50% of fleet credits (median 525 AIC
// against 96 for the rest), so the first compaction is the signal.
func DefaultRecycle() Recycle { return Recycle{Compactions: 1, AIC: 500} }

// Off reports whether both thresholds are disabled.
func (r Recycle) Off() bool { return r.Compactions <= 0 && r.AIC <= 0 }

// Due reports whether an agent with these figures has crossed a threshold.
func (r Recycle) Due(compactions int, aic float64) bool {
	return (r.Compactions > 0 && compactions >= r.Compactions) || (r.AIC > 0 && aic >= r.AIC)
}

// Recycle is the [defaults] recycle thresholds.
func (p *Prefs) Recycle() Recycle {
	if p == nil {
		return DefaultRecycle()
	}
	return p.recycle
}

func applyRecycle(r Recycle, sec map[string]conf.Value, path string) (Recycle, error) {
	if v := strings.TrimSpace(conf.Str(sec, "recycle_compactions")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return r, fmt.Errorf("%s: recycle_compactions %q: want a whole number >= 0", path, v)
		}
		r.Compactions = n
	}
	if v := strings.TrimSpace(conf.Str(sec, "recycle_aic")); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || n < 0 {
			return r, fmt.Errorf("%s: recycle_aic %q: want a number >= 0", path, v)
		}
		r.AIC = n
	}
	return r, nil
}
