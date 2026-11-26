package runs

import (
	"fmt"
	"io"
	"math"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
)

// MinRuns is the group size under which a scorecard row is marked "few":
// below it only a large difference means anything (ADR-170).
const MinRuns = 20

// Keys are the fields `fleet stats --runs --by` groups runs by.
var Keys = map[string]func(Record) string{
	"profile":   func(r Record) string { return r.Profile },
	"model":     func(r Record) string { return r.Model },
	"effort":    func(r Record) string { return r.Effort },
	"version":   func(r Record) string { return r.FleetVersion },
	"repo":      func(r Record) string { return r.Repo },
	"week":      func(r Record) string { y, w := r.Closed.ISOWeek(); return fmt.Sprintf("%d-W%02d", y, w) },
	"playbook":  func(r Record) string { return cfg(r).Playbook },
	"agents_md": func(r Record) string { return cfg(r).AgentsMD },
	"route":     func(r Record) string { return cfg(r).Route },
	"stage":     func(r Record) string { return r.Stage },
	"arm":       func(r Record) string { return cfg(r).Arm },
}

func cfg(r Record) board.RunConfig {
	if r.Config == nil {
		return board.RunConfig{}
	}
	return *r.Config
}

// Row is one group of a scorecard.
type Row struct {
	Key           string  `json:"key"`
	Runs          int     `json:"runs"`
	Done          int     `json:"done"`
	DoneLo        float64 `json:"done_lo"` // 95% Wilson interval of the done rate
	DoneHi        float64 `json:"done_hi"`
	Reported      int     `json:"reported"` // runs that reported done at least once
	Checked       int     `json:"checked"`  // runs whose done-check ran
	Passed        int     `json:"passed"`
	Rework        int     `json:"rework"`         // more than one done, or a done sent back
	MedianMinutes float64 `json:"median_minutes"` // start to first done, done runs only
	AIC           float64 `json:"aic"`
	Tokens        int64   `json:"tokens"` // from tools that report no credits
	Compactions   int     `json:"compactions"`
	Interventions int     `json:"interventions"`
	Intervened    int     `json:"intervened"` // runs with at least one intervention
	Few           bool    `json:"few"`        // under MinRuns
}

// Summarize groups the runs by key. Interactive runs are left out: the user
// ends them, so they have no done to count. Rows come largest first.
func Summarize(rs []Record, key func(Record) string) []Row {
	groups := map[string][]Record{}
	for _, r := range rs {
		if r.Interactive {
			continue
		}
		k := key(r)
		if k == "" {
			k = "-"
		}
		groups[k] = append(groups[k], r)
	}
	out := make([]Row, 0, len(groups))
	for k, g := range groups {
		out = append(out, summarize(k, g))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Runs != out[j].Runs {
			return out[i].Runs > out[j].Runs
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func summarize(k string, g []Record) Row {
	row := Row{Key: k, Runs: len(g), Few: len(g) < MinRuns}
	var mins []float64
	for _, r := range g {
		if r.Done() {
			row.Done++
			if !r.FirstDone.IsZero() {
				mins = append(mins, r.FirstDone.Sub(r.Created).Minutes())
			}
		}
		if r.Reports["done"] > 0 {
			row.Reported++
		}
		if r.CheckResult != "" {
			row.Checked++
			if r.CheckResult == "pass" {
				row.Passed++
			}
		}
		if r.Reports["done"] > 1 || r.DoneBounces > 0 {
			row.Rework++
		}
		if s := r.Spend; s != nil {
			row.AIC += s.AIC
			row.Tokens += s.Tokens
			row.Compactions += s.Compactions
		}
		for _, n := range r.Interventions {
			row.Interventions += n
		}
		if len(r.Interventions) > 0 {
			row.Intervened++
		}
	}
	row.DoneLo, row.DoneHi = Wilson(row.Done, row.Runs)
	row.MedianMinutes = median(mins)
	return row
}

// Wilson is the 95% Wilson score interval of k successes in n trials.
func Wilson(k, n int) (lo, hi float64) {
	if n == 0 {
		return 0, 0
	}
	const z = 1.96
	p, fn := float64(k)/float64(n), float64(n)
	d := 1 + z*z/fn
	c := (p + z*z/(2*fn)) / d
	h := z * math.Sqrt(p*(1-p)/fn+z*z/(4*fn*fn)) / d
	return math.Max(0, c-h), math.Min(1, c+h)
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sort.Float64s(xs)
	m := len(xs) / 2
	if len(xs)%2 == 1 {
		return xs[m]
	}
	return (xs[m-1] + xs[m]) / 2
}

// Write prints rows as a table. VERIFIED is the share of runs that reported
// done whose done-check ran (ADR-202); CHECK is passed/run. Spend per done run is AIC where the tools
// report credits, else tokens; the two units are never added.
func Write(w io.Writer, rows []Row) error {
	if len(rows) == 0 {
		_, err := io.WriteString(w, "no runs recorded\n")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "GROUP\tRUNS\tDONE\t95%\tVERIFIED\tCHECK\tREWORK\tTO DONE\tSPEND/DONE\tCOMPACT/RUN\tINTERV/RUN\t")
	for _, r := range rows {
		few := ""
		if r.Few {
			few = "few"
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%.0f-%.0f%%\t%s\t%s\t%d\t%s\t%s\t%.2f\t%.2f\t%s\n",
			r.Key, r.Runs, pct(r.Done, r.Runs), 100*r.DoneLo, 100*r.DoneHi, verified(r), frac(r.Passed, r.Checked),
			r.Rework, mins(r.MedianMinutes), perDone(r), per(r.Compactions, r.Runs), per(r.Interventions, r.Runs), few)
	}
	return tw.Flush()
}

// verified is the share of runs that reported done whose check ran; "-"
// when none reported done.
func verified(r Row) string {
	if r.Reported == 0 {
		return "-"
	}
	return pct(r.Checked, max(r.Reported, r.Checked))
}

func pct(k, n int) string { return fmt.Sprintf("%.0f%%", 100*float64(k)/float64(max(n, 1))) }

func frac(k, n int) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d", k, n)
}

func per(k, n int) float64 { return float64(k) / float64(max(n, 1)) }

func mins(m float64) string {
	if m == 0 {
		return "-"
	}
	return (time.Duration(m * float64(time.Minute))).Round(time.Minute).String()
}

func perDone(r Row) string {
	d := float64(max(r.Done, 1))
	switch {
	case r.AIC > 0:
		return fmt.Sprintf("%.0f AIC", r.AIC/d)
	case r.Tokens > 0:
		return fmt.Sprintf("%.0fk tok", float64(r.Tokens)/d/1000)
	}
	return "-"
}
