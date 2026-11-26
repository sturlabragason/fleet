package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/runs"
)

// estimateLevels are the effort levels fleet estimate prints by default.
var estimateLevels = []string{"light", "standard", "pro"}

// backtestRuns is how many recent done runs the doctor line and
// --backtest score by default (ADR-208).
const backtestRuns = 100

// cmdEstimate prints the wall-clock estimate a spawn at each effort level
// would get, with the median phases of its reference class, or with
// --backtest how well the estimator did on the latest done runs (ADR-208).
func cmdEstimate(args []string) error {
	fs := flag.NewFlagSet("estimate", flag.ContinueOnError)
	effort := fs.String("effort", "", "one effort level: light, standard or pro (default: all three)")
	backtest := fs.Bool("backtest", false, "score the estimator and the pinned table on the latest done runs")
	last := fs.Int("last", backtestRuns, "with --backtest, how many of the latest done runs to score")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rs, err := runs.Read(logx.Home(), time.Time{})
	if err != nil {
		return err
	}
	if *backtest {
		return writeBacktest(os.Stdout, rs, *last, *asJSON)
	}
	levels := estimateLevels
	if *effort != "" {
		levels = []string{*effort}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	repo, releases := repoReleases(ctx)
	return writeEstimates(os.Stdout, rs, levels, repo, releases, *asJSON)
}

// repoReleases is the base name of the current repo's main checkout (how the
// run ledger names repos) and the times of its annotated v* tags, which the
// release workflow creates (ADR-73); empty outside a git repo.
func repoReleases(ctx context.Context) (string, []time.Time) {
	common, err := exec.CommandContext(ctx, "git", "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return "", nil // not a git repo: no releases to join
	}
	repo := filepath.Base(filepath.Dir(strings.TrimSpace(string(common))))
	out, err := exec.CommandContext(ctx, "git", "for-each-ref", "--format=%(taggerdate:iso-strict)", "refs/tags/v*").Output()
	if err != nil {
		return repo, nil // no tags to read: phases go without release
	}
	var ts []time.Time
	for l := range strings.Lines(string(out)) {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(l)); err == nil {
			ts = append(ts, t)
		}
	}
	return repo, ts
}

type levelEstimate struct {
	Effort   string            `json:"effort"`
	Estimate *board.Estimate   `json:"estimate,omitempty"`
	Phases   runs.PhaseMedians `json:"phases"`
}

func writeEstimates(w io.Writer, rs []runs.Record, levels []string, repo string, releases []time.Time, asJSON bool) error {
	out := make([]levelEstimate, 0, len(levels))
	for _, l := range levels {
		le := levelEstimate{Effort: l, Phases: runs.Phases(rs, l, repo, releases)}
		if e, ok := runs.Estimate(rs, l); ok {
			le.Estimate = &e
		}
		out = append(out, le)
	}
	if asJSON {
		b, _ := json.MarshalIndent(out, "", "  ")
		_, err := fmt.Fprintln(w, string(b))
		return err
	}
	for _, le := range out {
		if le.Estimate == nil {
			fmt.Fprintf(w, "%s: no estimate (no runs and not in the pinned table)\n", le.Effort)
			continue
		}
		p := le.Phases
		fmt.Fprintf(w, "%s: %s; p10 %s\n", le.Effort, le.Estimate, board.Minutes(le.Estimate.P10))
		if p.Runs == 0 {
			continue
		}
		fmt.Fprintf(w, "  median phases of %d done runs: queue %s, work %s%s, done to close %s\n",
			p.Runs, board.Minutes(p.Queue), board.Minutes(p.Work), phaseOf("merge to done", p.ToDone, p.Merged), board.Minutes(p.Tail))
		if p.Committed+p.Released > 0 {
			fmt.Fprintf(w, "  %s%s\n", strings.TrimPrefix(phaseOf("start to first commit", p.FirstCommit, p.Committed), ", "),
				phaseOf("merge to release", p.Release, p.Released))
		}
	}
	return nil
}

// phaseOf is ", <name> <median> (<n> runs)", or "" when no run had it.
func phaseOf(name string, m float64, n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", %s %s (%d runs)", name, board.Minutes(m), n)
}

// tableOnly is the pinned effort table as an estimator: the baseline the
// reference class is scored against.
func tableOnly(_ []runs.Record, effort string) (board.Estimate, bool) {
	return runs.TableEstimate(effort)
}

func writeBacktest(w io.Writer, rs []runs.Record, last int, asJSON bool) error {
	rows := []struct {
		Method string     `json:"method"`
		Score  runs.Score `json:"score"`
	}{
		{"reference class (effort)", runs.Backtest(rs, last, runs.Estimate)},
		{"pinned table", runs.Backtest(rs, last, tableOnly)},
	}
	if asJSON {
		b, _ := json.MarshalIndent(rows, "", "  ")
		_, err := fmt.Fprintln(w, string(b))
		return err
	}
	fmt.Fprintf(w, "the latest %d done runs, each estimated only from the runs closed before it was spawned\n", last)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "method\truns\tinside p10-p90 (80% is calibrated)\tmedian actual/p50\twithin 2x of p50\tlog pinball loss")
	for _, r := range rows {
		s := r.Score
		fmt.Fprintf(tw, "%s\t%d\t%.0f%%\t%.2f\t%.0f%%\t%.2f\n", r.Method, s.Runs, 100*s.Inside, s.MedianRatio, 100*s.Within2x, s.Pinball)
	}
	return tw.Flush()
}

// checkEstimates is the doctor line for the estimator's calibration on the
// latest done runs (ADR-208); false with no runs to score.
func checkEstimates(home string) (doctorCheck, bool) {
	rs, err := runs.Read(home, time.Time{})
	if err != nil {
		return doctorCheck{Name: "estimates", Level: levelWarn, Detail: err.Error()}, true
	}
	s := runs.Backtest(rs, backtestRuns, runs.Estimate)
	if s.Runs == 0 {
		return doctorCheck{}, false
	}
	return doctorCheck{Name: "estimates", Level: levelInfo, Detail: fmt.Sprintf(
		"%.0f%% of the latest %d done runs finished inside their p10-p90 estimate (80%% is calibrated), median actual/p50 %.2f; fleet estimate --backtest",
		100*s.Inside, s.Runs, s.MedianRatio)}, true
}
