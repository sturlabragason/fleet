package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/spend"
)

// cmdSpend prints what each agent has cost and the fleet-wide total, read
// from the agents' own session files (§7b, ADR-46). No model is called.
func cmdSpend(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("spend", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
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

// calls renders a call count, or "-" for a tool that keeps a running total
// without one.
func calls(n int) string {
	if n == 0 {
		return "-"
	}
	return strconv.Itoa(n)
}
