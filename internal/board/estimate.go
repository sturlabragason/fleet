package board

import "fmt"

// Estimate is the wall-clock forecast fleet makes for an agent at spawn
// (ADR-208): minutes from spawn to its first done report, as quantiles of the
// reference class of past runs that Basis names.
type Estimate struct {
	Basis string  `json:"basis"`       // the effort level whose runs it used, "all" (every level) or "table" (the pinned cold start)
	N     int     `json:"n,omitempty"` // done runs in the reference class; 0 for the table
	P10   float64 `json:"p10"`
	P50   float64 `json:"p50"`
	P90   float64 `json:"p90"`
}

// String is the estimate as a spawn reply shows it.
func (e Estimate) String() string {
	from := "pinned table, too few past runs"
	switch e.Basis {
	case "table":
	case "all":
		from = fmt.Sprintf("%d past runs of every level", e.N)
	default:
		from = fmt.Sprintf("%d past %s runs", e.N, e.Basis)
	}
	return fmt.Sprintf("estimate p50 %s, p90 %s (%s)", Minutes(e.P50), Minutes(e.P90), from)
}

// Minutes formats a duration in minutes for a person: "0.4 min", "23 min",
// "2.5 h".
func Minutes(m float64) string {
	switch {
	case m < 10:
		return fmt.Sprintf("%.1f min", m)
	case m < 120:
		return fmt.Sprintf("%.0f min", m)
	}
	return fmt.Sprintf("%.1f h", m/60)
}
