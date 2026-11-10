package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/worktree"
)

// cmdWorktrees lists the repo's leftover worktrees under <repo>.wt/ and,
// with --prune, removes the merged, clean ones nobody holds (ADR-79).
func cmdWorktrees(f *fleet.Fleet, args []string) error {
	return runWorktrees(f.Store.Load(), args, os.Stdout)
}

func runWorktrees(b *board.Board, args []string, out io.Writer) error {
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
	_, err := fmt.Fprintf(out, "%d worktrees, %s; %d can be pruned (merged, clean, no live or parked agent): fleet worktrees --prune\n", len(es), worktree.HumanSize(total), pr)
	return err
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
