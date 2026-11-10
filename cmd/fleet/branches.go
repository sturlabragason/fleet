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

// cmdBranches lists the repo's fleet/ branches on origin and, with --prune,
// deletes the merged ones no live or parked agent is on (ADR-85).
func cmdBranches(f *fleet.Fleet, args []string) error {
	return runBranches(f.Store.Load(), args, os.Stdout)
}

func runBranches(b *board.Board, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("branches", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo dir (default: the current directory)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	prune := fs.Bool("prune", false, "delete the merged fleet/ branches no agent is on from "+worktree.Remote)
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := *repo
	if dir == "" {
		dir, _ = os.Getwd()
	}
	main, err := worktree.Main(dir)
	if err != nil {
		return err
	}
	held := map[string]string{}
	for _, a := range b.Agents {
		st := "live"
		if a.Status == string(status.Parked) {
			st = "parked"
		}
		// An agent's branch is on its board entry; one that made its own
		// worktree by hand is on fleet/<name> by the playbook's rule.
		held[worktree.BranchPrefix+a.Name] = st
		if a.Worktree != nil && a.Worktree.Branch != "" {
			held[a.Worktree.Branch] = st
		}
	}
	bs, err := worktree.RemoteBranches(main, held)
	if err != nil {
		return err
	}
	if *prune {
		done, err := worktree.PruneRemote(main, bs)
		for _, n := range done {
			fmt.Fprintf(out, "deleted %s/%s\n", worktree.Remote, n)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%d remote branches deleted\n", len(done))
		return nil
	}
	if *asJSON {
		if bs == nil {
			bs = []worktree.RemoteBranch{}
		}
		e := json.NewEncoder(out)
		e.SetIndent("", "  ")
		return e.Encode(bs)
	}
	if len(bs) == 0 {
		_, err := fmt.Fprintf(out, "no %s branches on %s\n", worktree.BranchPrefix, worktree.Remote)
		return err
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "BRANCH\tMERGED\tAGENT")
	pr := 0
	for _, x := range bs {
		agent := "-"
		if x.Agent != "" {
			agent = x.Agent
		}
		if x.Prunable {
			pr++
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", x.Branch, yesNo(x.Merged), agent)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%d branches on %s; %d can be deleted (merged, no live or parked agent): fleet branches --prune\n", len(bs), worktree.Remote, pr)
	return err
}
