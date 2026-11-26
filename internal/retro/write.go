package retro

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/sturlabragason/fleet/internal/runs"
)

// maxPlaybooks is how many playbook groups the text report shows, largest
// first; each playbook edit makes a new hash, so the tail is small groups.
const maxPlaybooks = 10

// Write prints the report: runs by playbook, the active trial's arms and
// verdict, then the candidate patches.
func Write(w io.Writer, r Report) error {
	fmt.Fprintf(w, "Runs by playbook since %s\n", r.Since.Format("2006-01-02"))
	rows := r.Playbooks[:min(len(r.Playbooks), maxPlaybooks)]
	if err := runs.Write(w, rows); err != nil {
		return err
	}
	if n := len(r.Playbooks) - len(rows); n > 0 {
		fmt.Fprintf(w, "(%d smaller groups left out; --json has all)\n", n)
	}
	if r.Trial != nil {
		writeTrial(w, *r.Trial, *r.Verdict)
	}
	fmt.Fprintf(w, "\nRules the ledger can see (%d runs in the window; a rule is a deletion candidate at 0 fired and %d+ runs)\n", r.Runs, MinArmRuns)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "RULE\tKINDS\tFIRED\t")
	for _, x := range r.Rules {
		mark := ""
		if r.candidate(x) {
			mark = "candidate: delete"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", x.Anchor, strings.Join(x.Kinds, ","), x.Fired, mark)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(w, "\nRecurring retro themes (%d retro: lines; a theme is a word in %d+ of them)\n", r.RetroLines, MinTheme)
	if len(r.Themes) == 0 {
		_, err := io.WriteString(w, "none\n")
		return err
	}
	tw = tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	for _, t := range r.Themes {
		fmt.Fprintf(tw, "%s\t%d\t%s\n", t.Word, t.Lines, t.Example)
	}
	return tw.Flush()
}

func writeTrial(w io.Writer, t Trial, v Verdict) {
	fmt.Fprintf(w, "\nTrial %s: variant on %d%% of new spawns since %s, from %s\n", t.ID, t.Share, t.Started.Format("2006-01-02 15:04"), t.Source)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ARM\tRUNS\tDONE 95%\tREWORK 95%\tINTERVENED 95%\t")
	for _, row := range []runs.Row{v.Control, v.Variant} {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", row.Key, row.Runs,
			interval(row.Done, row.Runs), interval(row.Rework, row.Runs), interval(row.Intervened, row.Runs))
	}
	_ = tw.Flush()
	switch v.State {
	case Pending:
		fmt.Fprintf(w, "verdict: pending, each arm needs %d closed runs (control %d, variant %d)\n", MinArmRuns, v.Control.Runs, v.Variant.Runs)
	case Worse:
		fmt.Fprintf(w, "verdict: worse on %s; stop it with `fleet retro trial --stop`\n", strings.Join(v.Worse, ", "))
	default:
		fmt.Fprintln(w, "verdict: not worse on done rate, rework or interventions; a human may run `fleet retro promote`")
	}
}

// interval is "k/n lo-hi%", the 95% Wilson interval of k in n.
func interval(k, n int) string {
	if n == 0 {
		return "-"
	}
	lo, hi := runs.Wilson(k, n)
	return fmt.Sprintf("%d/%d %.0f-%.0f%%", k, n, 100*lo, 100*hi)
}
