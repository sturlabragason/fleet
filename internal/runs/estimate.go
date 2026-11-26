package runs

import (
	"math"
	"slices"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
)

// MinClass is the number of done runs a reference class needs before its
// quantiles are used (ADR-208; 4 to 20 scored the same on held-out runs).
const MinClass = 8

// Table is the effort-class calibration pinned on 2026-11-26 (p10, p50, p90
// minutes): the cold start while a level has fewer than MinClass done runs.
var Table = map[string][3]float64{"light": {0.5, 1, 2}, "standard": {3, 5, 8}, "pro": {20, 27, 35}}

// Minutes is a run's wall clock from spawn to its first done report, queue
// wait included; false for a run with no done or an interactive one.
func Minutes(r Record) (float64, bool) {
	if r.Interactive || !r.FirstDone.After(r.Created) {
		return 0, false
	}
	return r.FirstDone.Sub(r.Created).Minutes(), true
}

// Estimate forecasts a run at an effort level from past runs (ADR-208): the
// quantiles of the done runs at that level, else the pinned table, else the
// quantiles of every done run. Effort alone is the reference class because
// repo, route, profile, model and code-or-not each scored worse on held-out
// runs. false: nothing to go on.
func Estimate(past []Record, effort string) (board.Estimate, bool) {
	var class, all []float64
	for _, r := range past {
		m, ok := Minutes(r)
		if !ok {
			continue
		}
		all = append(all, m)
		if r.Effort == effort {
			class = append(class, m)
		}
	}
	if len(class) >= MinClass {
		return quantiles(effort, class), true
	}
	if t, ok := TableEstimate(effort); ok {
		return t, true
	}
	if len(all) >= MinClass {
		return quantiles("all", all), true
	}
	return board.Estimate{}, false
}

// TableEstimate is the pinned table's range for an effort level.
func TableEstimate(effort string) (board.Estimate, bool) {
	t, ok := Table[effort]
	return board.Estimate{Basis: "table", P10: t[0], P50: t[1], P90: t[2]}, ok
}

func quantiles(basis string, xs []float64) board.Estimate {
	slices.Sort(xs)
	return board.Estimate{Basis: basis, N: len(xs), P10: quantile(xs, .1), P50: quantile(xs, .5), P90: quantile(xs, .9)}
}

// quantile of sorted xs by linear interpolation (Hyndman and Fan type 7,
// the default in R and numpy).
func quantile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	h := p * float64(len(xs)-1)
	lo := int(h)
	if lo+1 >= len(xs) {
		return xs[len(xs)-1]
	}
	return xs[lo] + (h-float64(lo))*(xs[lo+1]-xs[lo])
}

// Phases splits a done run's wall clock (ADR-208): queued before it started,
// work up to its merge into main (up to its first done when fleet saw no
// merge), merge to first done (release, result file, report) and first done
// to close.
func (r Record) Phases() (queue, work, merged, tail time.Duration) {
	queue = time.Duration(r.TimeS["queued"]) * time.Second
	end := r.FirstDone
	if r.mergedBeforeDone() {
		end, merged = r.Merged, r.FirstDone.Sub(r.Merged)
	}
	work = max(end.Sub(r.Created)-queue, 0)
	if r.Closed.After(r.FirstDone) {
		tail = r.Closed.Sub(r.FirstDone)
	}
	return queue, work, merged, tail
}

// mergedBeforeDone reports whether fleet saw the run's merge between its
// spawn and its first done; a merge after the done (a later queue) is not
// part of its time to done.
func (r Record) mergedBeforeDone() bool {
	return r.Merged.After(r.Created) && !r.Merged.After(r.FirstDone)
}

// PhaseMedians are the median minutes of each phase (Record.Phases) of an
// effort level's done runs, with the medians of start to first commit and
// merge to release over the runs that have them.
type PhaseMedians struct {
	Queue       float64 `json:"queue"`
	Work        float64 `json:"work"`
	ToDone      float64 `json:"merge_to_done"`
	Tail        float64 `json:"done_to_close"`
	FirstCommit float64 `json:"start_to_first_commit"`
	Release     float64 `json:"merge_to_release"`
	Runs        int     `json:"runs"`
	Merged      int     `json:"merged"`
	Committed   int     `json:"committed"`
	Released    int     `json:"released"`
}

// Phases is the median phase split of the done runs at an effort level;
// releases are the times of the repo's releases, and a merged run of repo
// counts as released by the first one after its merge.
func Phases(rs []Record, effort, repo string, releases []time.Time) PhaseMedians {
	var q, wk, td, tl, fc, rl []float64
	for _, r := range rs {
		if _, ok := Minutes(r); !ok || r.Effort != effort {
			continue
		}
		a, b, c, d := r.Phases()
		q, wk, tl = append(q, a.Minutes()), append(wk, b.Minutes()), append(tl, d.Minutes())
		if r.mergedBeforeDone() {
			td = append(td, c.Minutes())
		}
		if r.FirstCommit.After(r.Created) {
			fc = append(fc, max(r.FirstCommit.Sub(r.Created)-a, 0).Minutes())
		}
		if t, ok := releasedAt(r, repo, releases); ok {
			rl = append(rl, t.Sub(r.Merged).Minutes())
		}
	}
	return PhaseMedians{Queue: median(q), Work: median(wk), ToDone: median(td), Tail: median(tl),
		FirstCommit: median(fc), Release: median(rl),
		Runs: len(q), Merged: len(td), Committed: len(fc), Released: len(rl)}
}

// releasedAt is the first of releases after r's merge, for a merged run of
// repo; releases are cut per batch, so it is when r's change shipped.
func releasedAt(r Record, repo string, releases []time.Time) (time.Time, bool) {
	if r.Merged.IsZero() || repo == "" || r.Repo != repo {
		return time.Time{}, false
	}
	var first time.Time
	for _, t := range releases {
		if t.After(r.Merged) && (first.IsZero() || t.Before(first)) {
			first = t
		}
	}
	return first, !first.IsZero()
}

// Score is how estimates matched the actual minutes of a set of runs.
type Score struct {
	Runs        int     `json:"runs"`
	Inside      float64 `json:"inside"`       // share of actuals within p10..p90; 0.8 is calibrated
	MedianRatio float64 `json:"median_ratio"` // median of actual / p50; 1 is unbiased
	Within2x    float64 `json:"within_2x"`    // share of actuals within half to twice p50
	Pinball     float64 `json:"pinball"`      // mean quantile loss of p10, p50, p90 in log minutes; lower is better
}

// Backtest scores an estimator on the last n done runs of rs (prequential,
// ADR-208): each run is estimated only from the runs that closed before it
// was spawned, as fleet would have at its spawn.
func Backtest(rs []Record, n int, est func(past []Record, effort string) (board.Estimate, bool)) Score {
	var test []Record
	for _, r := range rs {
		if _, ok := Minutes(r); ok {
			test = append(test, r)
		}
	}
	slices.SortStableFunc(test, func(a, b Record) int { return a.Created.Compare(b.Created) })
	if n > 0 && len(test) > n {
		test = test[len(test)-n:]
	}
	var s Score
	var ratios []float64
	for _, r := range test {
		var past []Record
		for _, p := range rs {
			if p.Closed.Before(r.Created) {
				past = append(past, p)
			}
		}
		e, ok := est(past, r.Effort)
		if !ok || e.P50 <= 0 {
			continue
		}
		a, _ := Minutes(r)
		s.Runs++
		if e.P10 <= a && a <= e.P90 {
			s.Inside++
		}
		ratios = append(ratios, a/e.P50)
		if a >= e.P50/2 && a <= e.P50*2 {
			s.Within2x++
		}
		s.Pinball += pinball(a, e)
	}
	if s.Runs == 0 {
		return s
	}
	k := float64(s.Runs)
	slices.Sort(ratios)
	s.Inside, s.Within2x, s.Pinball, s.MedianRatio = s.Inside/k, s.Within2x/k, s.Pinball/k, quantile(ratios, .5)
	return s
}

// pinball is the quantile (pinball) loss of e's three quantiles for actual
// a, in log minutes so a 2x miss costs the same at 1 min as at 1 h.
func pinball(a float64, e board.Estimate) float64 {
	var sum float64
	for _, q := range [][2]float64{{.1, e.P10}, {.5, e.P50}, {.9, e.P90}} {
		d := math.Log(a) - math.Log(max(q[1], 0.01))
		sum += max(q[0]*d, (q[0]-1)*d)
	}
	return sum
}
