package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/spend"
	ubar "github.com/sturlabragason/fleet/internal/usage"
)

// cmdSpend prints what each agent has cost and the fleet-wide total, read
// from the agents' own session files (§7b, ADR-46). No model is called.
func cmdSpend(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("spend", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	today := fs.Bool("today", false, "per-profile usage today and the Copilot month (the usage bar's figures, with raw units)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *today {
		now := time.Now()
		c := f.UsageConfig()
		return writeUsage(os.Stdout, c, f.ObserveUsage(now, c), now, *asJSON)
	}
	rows, total := f.SpendAll()
	return writeSpend(os.Stdout, rows, total, *asJSON)
}

func writeSpend(out io.Writer, rows []fleet.AgentSpend, total spend.Spend, asJSON bool) error {
	if asJSON {
		e := json.NewEncoder(out)
		e.SetIndent("", "  ")
		return e.Encode(struct {
			Agents []fleet.AgentSpend `json:"agents"`
			Total  spend.Spend        `json:"total"`
			Note   string             `json:"note"`
		}{rows, total, fleet.SpendNote})
	}
	if len(rows) == 0 {
		_, err := io.WriteString(out, "no agents\n")
		return err
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPROFILE\tCALLS\tTOKENS\tSPEND")
	for _, r := range rows {
		if !r.Known {
			fmt.Fprintf(w, "%s\t%s\t-\t-\tno usage recorded\n", r.Name, r.Profile)
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.Name, r.Profile, calls(r.Calls), spend.Tokens(r.Total()), r.Short())
	}
	fmt.Fprintf(w, "TOTAL\t\t%s\t%s\t%s\n", calls(total.Calls), spend.Tokens(total.Total()), total.Short())
	if err := w.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "\n%s\n", fleet.SpendNote)
	return err
}

// writeUsage is the details view behind the usage bar (ADR-97): the day
// ledger's tallies per profile with raw credits and tokens.
func writeUsage(out io.Writer, c ubar.Config, l *ubar.Ledger, now time.Time, asJSON bool) error {
	today := l.Today(now)
	monthAIC := l.MonthAIC(now)
	if asJSON {
		e := json.NewEncoder(out)
		e.SetIndent("", "  ")
		return e.Encode(struct {
			Day             string                 `json:"day"`
			Profiles        map[string]ubar.Tally `json:"profiles"`
			CopilotMonthAIC float64                `json:"copilot_month_aic"`
			CopilotAllow    float64                `json:"copilot_month_allowance"`
		}{ubar.Day(now), today, monthAIC, c.CopilotMonthAIC})
	}
	names := make([]string, 0, len(today))
	for n := range today {
		names = append(names, n)
	}
	sort.Strings(names)
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintf(w, "PROFILE\tAIC\tTOKENS\tEST. $ TODAY\n")
	for _, n := range names {
		t := today[n]
		aic, cost := "-", ubar.USD(t.USD)
		if t.Kind == spend.Copilot {
			aic = spend.Credits(t.AIC)
		}
		if t.Unpriced {
			cost = "no price for the model"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", n, aic, spend.Tokens(t.Tokens), cost)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Fprintln(out, "nothing spent today")
	}
	if pct, over, ok := c.CopilotPct(monthAIC); ok {
		line := fmt.Sprintf("\nCopilot this month: %s of %s AIC (%d%%)", spend.Credits(monthAIC), spend.Credits(c.CopilotMonthAIC), pct)
		if over > 0 {
			line += ", " + ubar.USD(over) + " over the allowance"
		}
		fmt.Fprintln(out, line)
	}
	_, err := fmt.Fprintln(out, "\nfleet agents only, counted from when fleet first saw each session; $ is list price, not your bill")
	return err
}

// calls renders a call count, or "-" for a tool that keeps a running total
// without one.
func calls(n int) string {
	if n == 0 {
		return "-"
	}
	return strconv.Itoa(n)
}
