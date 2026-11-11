package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/worktree"
)

// cmdWorktrees lists the repo's leftover worktrees under <repo>.wt/ and,
// with --prune, removes the merged, clean ones nobody holds (ADR-79).
func cmdWorktrees(f *fleet.Fleet, args []string) error {
	b, bad := loadBoard(f.Store)
	return runWorktrees(b, bad, args, os.Stdout)
}

// bad is why the board or the closed records could not be read: listing
// goes on, a prune is refused.
func runWorktrees(b *board.Board, bad error, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("worktrees", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo dir (default: the current directory)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	prune := fs.Bool("prune", false, "remove the merged, clean worktrees no agent holds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	dir := *repo
	if dir == "" {
		dir = cwd
	}
	main, err := worktree.Main(dir)
	if err != nil {
		return err
	}
	held := map[string]string{}
	for _, a := range b.Agents {
		if a.Worktree == nil || a.Worktree.Path == "" {
			continue
		}
		held[a.Worktree.Path] = "live"
		if a.Status == string(status.Parked) {
			held[a.Worktree.Path] = "parked"
		}
	}
	if err := holdClosed(b, bad, *prune, func(a *board.Agent) {
		if a.Worktree != nil && a.Worktree.Path != "" && held[a.Worktree.Path] == "" {
			held[a.Worktree.Path] = "closed"
		}
	}); err != nil {
		return err
	}
	es, err := worktree.Stale(main, cwd, held)
	if err != nil {
		return err
	}
	if *prune {
		for _, r := range worktree.Prune(main, es) {
			switch {
			case r.Err != nil:
				fmt.Fprintf(out, "kept %s: %v\n", r.Entry.Path, r.Err)
			case r.Entry.Gone:
				fmt.Fprintf(out, "pruned %s (directory gone)\n", r.Entry.Path)
			case r.Branch != "":
				fmt.Fprintf(out, "removed %s (%s), branch %s deleted\n", r.Entry.Path, worktree.HumanSize(r.Entry.Bytes), r.Branch)
			default:
				fmt.Fprintf(out, "removed %s (%s)\n", r.Entry.Path, worktree.HumanSize(r.Entry.Bytes))
			}
		}
		return nil
	}
	if *asJSON {
		if es == nil {
			es = []worktree.Entry{}
		}
		e := json.NewEncoder(out)
		e.SetIndent("", "  ")
		return e.Encode(es)
	}
	return writeWorktrees(out, es)
}

func writeWorktrees(out io.Writer, es []worktree.Entry) error {
	if len(es) == 0 {
		_, err := io.WriteString(out, "no worktrees\n")
		return err
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tBRANCH\tSIZE\tMERGED\tDIRTY\tAGENT")
	var total int64
	pr := 0
	for _, e := range es {
		total += e.Bytes
		agent := "-"
		if e.Agent != "" {
			agent = e.Agent
		}
		merged, dirty, size := "?", yesNo(e.Dirty), worktree.HumanSize(e.Bytes)
		if e.ResultOnly {
			dirty = "result.md"
		}
		if e.Merged != nil {
			merged = yesNo(*e.Merged)
		}
		if e.Unknown {
			dirty = "?"
		}
		if e.Gone {
			merged, dirty, size = "-", "-", "gone"
		}
		if e.Prunable {
			pr++
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Name, e.Branch, size, merged, dirty, agent)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "%d worktrees, %s; %d can be pruned (merged, clean, no live, parked or recently closed agent): fleet worktrees --prune\n", len(es), worktree.HumanSize(total), pr)
	return err
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// loadBoard is the board and, when board.json or closed.json cannot be
// read, why: a broken file reads as no agents, which a prune must not trust.
func loadBoard(s *board.Store) (*board.Board, error) {
	b, err := s.LoadChecked()
	if err != nil {
		return s.Load(), err
	}
	return b, nil
}

// holdClosed calls hold with each agent closed within board.ClosedKeep:
// `fleet resume` can still bring it back into its worktree and branch, so a
// prune must keep them (ADR-113). When the board or the records cannot be
// read (bad), a prune is refused rather than run as if there were no agents.
func holdClosed(b *board.Board, bad error, prune bool, hold func(*board.Agent)) error {
	if bad == nil {
		bad = b.ClosedErr()
	}
	if bad != nil {
		if prune {
			return fmt.Errorf("agents unknown, not pruning: %w", bad)
		}
		return nil
	}
	now := time.Now()
	for _, c := range b.ClosedRecords() {
		if now.Sub(c.At) <= board.ClosedKeep {
			hold(&c.Agent)
		}
	}
	return nil
}
