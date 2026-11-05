package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/sturlabragason/fleet/internal/adr"
	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/sessions"
)

// currentAgent names the agent whose pane this runs in, read from the board
// alone so "fleet adr" needs no tmux. "" when it is not an agent pane.
func currentAgent() string {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return ""
	}
	for name, a := range board.NewStore(logx.Home()).Load().Agents {
		if a.PaneID == pane {
			return name
		}
	}
	return ""
}

// cmdADR runs "fleet adr claim|ls|release". It needs no tmux and no board, so
// an agent can take a number from any shell (DESIGN.md §14, ADR-50).
func cmdADR(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fleet adr claim [--for AGENT] | fleet adr ls [--json] | fleet adr release N")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("adr "+sub, flag.ContinueOnError)
	dir := fs.String("C", ".", "directory inside the repo whose DESIGN.md holds the ADRs")
	// Each subcommand takes only its own flags, so "adr release 51 --for x"
	// fails instead of quietly ignoring half of what was typed.
	forAgent, asJSON := new(string), new(bool)
	switch sub {
	case "claim":
		forAgent = fs.String("for", "", "the agent the number is for (default: this pane's agent)")
	case "ls", "list":
		asJSON = fs.Bool("json", false, "JSON output")
	}
	pos, err := parseInterleaved(fs, rest)
	if err != nil {
		return err
	}
	s := adr.NewStore(logx.Home())
	switch sub {
	case "claim":
		if len(pos) != 0 {
			return fmt.Errorf("usage: fleet adr claim [--for AGENT] [-C dir]")
		}
		onMain, err := adr.OnMain(*dir)
		if err != nil {
			return err
		}
		who := *forAgent
		if who == "" {
			who = currentAgent()
		}
		n, err := s.Claim(who, onMain)
		if err != nil {
			return err
		}
		fmt.Println(n)
		return nil
	case "ls", "list":
		if len(pos) != 0 {
			return fmt.Errorf("usage: fleet adr ls [--json] [-C dir]")
		}
		onMain, err := adr.OnMain(*dir)
		if err != nil {
			return err
		}
		cs, err := s.List(onMain)
		if err != nil {
			return err
		}
		if *asJSON {
			e := json.NewEncoder(os.Stdout)
			e.SetIndent("", "  ")
			return e.Encode(cs)
		}
		if len(cs) == 0 {
			fmt.Println("no live ADR claims")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "ADR\tFOR\tAGE")
		for _, c := range cs {
			who := c.For
			if who == "" {
				who = "-"
			}
			fmt.Fprintf(w, "%d\t%s\t%s\n", c.N, who, sessions.Age(time.Since(c.Created)))
		}
		return w.Flush()
	case "release":
		if len(pos) != 1 {
			return fmt.Errorf("usage: fleet adr release N")
		}
		n, err := strconv.Atoi(pos[0])
		if err != nil {
			return fmt.Errorf("bad ADR number %q", pos[0])
		}
		if err := s.Release(n); err != nil {
			return err
		}
		fmt.Printf("released ADR-%d\n", n)
		return nil
	}
	return fmt.Errorf("unknown adr command %q: claim, ls or release", sub)
}
