package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sturlabragason/fleet/internal/adr"
	"github.com/sturlabragason/fleet/internal/companion"
	"github.com/sturlabragason/fleet/internal/graph"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/sysx"
	"github.com/sturlabragason/fleet/internal/worktree"
)

// cmdMerge rebases this worktree's branch onto main, runs the checks, merges
// into the main checkout and runs them again (ADR-48). The check and the
// append-only files come from the repo's .fleet/repo.toml (ADR-155). Exit 1: a
// conflict outside the append-only files, or a failed check.
func cmdMerge(args []string) error {
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	dir := fs.String("C", ".", "directory inside the worktree to merge")
	dry := fs.Bool("dry-run", false, "print the order this would take and stop")
	asJSON := fs.Bool("json", false, "with --dry-run, print the plan as JSON")
	check := fs.String("check", "", "shell command to run on the branch and on main (default: merge_check in "+
		worktree.RepoConfigFile+", else "+worktree.DefaultMergeCheck+"; empty skips)")
	retries := fs.Int("retries", 3, "attempts when main moves during the run")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := repoConfig(*dir)
	if err != nil {
		return fail(err)
	}
	if !flagSet(fs, "check") {
		*check = cfg.MergeCheck
	}
	if *dry {
		p, err := worktree.NewPlan(*dir, resultFiles()...)
		if err != nil {
			return fail(err)
		}
		taken := adrTaken(p.Dir, p.Branch)
		if *asJSON {
			out := struct {
				*worktree.Plan
				ADRTaken []string `json:"adr_taken,omitempty"`
			}{Plan: p}
			for _, c := range taken {
				out.ADRTaken = append(out.ADRTaken, c.String())
			}
			b, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		steps := append(p.Steps(), graphSteps(context.Background(), p.Dir)...)
		if len(taken) > 0 {
			steps = append([]string{adrFix(taken)}, steps...)
		}
		for i, s := range steps {
			fmt.Printf("%d. %s\n", i+1, s)
		}
		return nil
	}
	// A number that is taken would land as a second entry with the same
	// number; stop before the rebase so the fix goes through the lock.
	if p, err := worktree.NewPlan(*dir, resultFiles()...); err == nil {
		if taken := adrTaken(p.Dir, p.Branch); len(taken) > 0 {
			return fail(errors.New(adrFix(taken)))
		}
	}
	opts := worktree.Options{Dir: *dir, Retries: *retries, Ignore: resultFiles(), AppendOnly: cfg.AppendOnly, Log: func(s string) {
		fmt.Fprintln(os.Stderr, "fleet merge:", s)
	}}
	if strings.TrimSpace(*check) != "" {
		// The default is fleet's own gate. In a repo without a Makefile it
		// would fail before anything is merged, so say what to pass instead
		// of merging untested work.
		if *check == worktree.DefaultMergeCheck && !hasMakefile(*dir) {
			return fail(fmt.Errorf("no Makefile here, so the default check (%s) can't run:\n"+
				"  set merge_check in %s, pass --check 'go build ./... && go vet ./...', or --check '' to skip it",
				worktree.DefaultMergeCheck, worktree.RepoConfigFile))
		}
		opts.Check = func(d string) error { return shellCheck(d, *check) }
	}
	p, err := worktree.Merge(opts)
	if err != nil {
		return fail(err)
	}
	fmt.Printf("merged %s into %s in %s\n", p.Branch, p.Base, p.BaseDir)
	fmt.Println("worktree and branch left in place; nothing pushed")
	rebuildGraph(p.BaseDir)
	return nil
}

// graphSteps are the dry run's code-graph lines (ADR-165): the other
// worktrees this branch's changes overlap or are one dependency away from.
// None without a fresh graph.
func graphSteps(ctx context.Context, dir string) []string {
	if _, err := graph.Fresh(ctx, logx.Home(), dir); err != nil {
		return nil
	}
	cs, err := graph.Touching(ctx, logx.Home(), dir, dir, nil, resultFiles(), "merge", true)
	if err != nil {
		logx.For("graphify").Warn("merge dry-run graph check failed", "err", err)
		return nil
	}
	return graph.MergeSteps(cs)
}

// repoConfig reads the repo's merge conventions, with a one-line note when it
// has none and fleet's own layout applies.
func repoConfig(dir string) (worktree.RepoConfig, error) {
	c, err := worktree.LoadRepoConfig(dir)
	if err != nil {
		return c, err
	}
	if !c.Found {
		fmt.Fprintf(os.Stderr, "fleet merge: no %s; using fleet's defaults (check %q, append-only CHANGELOG.md and DESIGN.md)\n",
			worktree.RepoConfigFile, c.MergeCheck)
	}
	return c, nil
}

func flagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

// graphAfterMerge: rebuild the code graph in the background when the build
// mode is "merge" and graphify is usable (DESIGN §6.15).
func graphAfterMerge(mode string, found bool) bool { return mode == "merge" && found }

// rebuildGraph starts `fleet graph -C dir` detached. It never fails the merge.
func rebuildGraph(dir string) {
	pr, _ := prefs.Load()
	_, err := companion.Find(graph.Spec)
	if !graphAfterMerge(graph.BuildMode(pr), err == nil) {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		logx.For("graphify").Warn("graph rebuild not started", "err", err)
		return
	}
	cmd := exec.Command(exe, "graph", "-C", dir)
	sysx.Detach(cmd)
	if err := cmd.Start(); err != nil {
		logx.For("graphify").Warn("graph rebuild not started", "err", err)
		return
	}
	fmt.Println("fleet merge: rebuilding the code graph in the background (fleet graph)")
}

// fail prints the error, naming the other worktrees when it is a conflict, and
// ends the process with status 1.
func fail(err error) error {
	var c *worktree.ConflictError
	if errors.As(err, &c) {
		fmt.Fprintln(os.Stderr, "fleet merge: "+c.Error())
		fmt.Fprintln(os.Stderr, "fleet merge: report blocked with those files; your branch is unchanged")
	} else {
		fmt.Fprintln(os.Stderr, "fleet merge:", err)
	}
	return exitCode(1)
}

// adrTaken returns the ADR numbers this branch adds that are already on main
// or held by another agent's live claim (fleet#40). A repo without ADRs, or
// one fleet can't read, has none.
func adrTaken(dir, branch string) []adr.Collision {
	adds, err := adr.BranchAdds(dir)
	if err != nil || len(adds) == 0 {
		return nil
	}
	onMain, err := adr.OnMain(dir)
	if err != nil {
		return nil
	}
	claims, err := adr.NewStore(logx.Home()).List(onMain)
	if err != nil {
		logx.For("merge").Warn("adr claims unreadable; checking main only", "err", err)
	}
	return adr.Taken(adds, onMain, claims, currentAgent(), strings.TrimPrefix(branch, "fleet/"))
}

// adrFix says which numbers are taken and that the new one must come from the
// claim lock, never from "the next free one in DESIGN.md".
func adrFix(taken []adr.Collision) string {
	var parts []string
	for _, c := range taken {
		parts = append(parts, c.String())
	}
	return strings.Join(parts, "; ") + ": run `fleet adr claim` and renumber your ADR to the number it prints " +
		"(the §14 entry, the CHANGELOG bracket and the commit), then fleet merge again"
}

func hasMakefile(dir string) bool {
	top, err := worktree.TopLevel(dir)
	if err != nil {
		return false
	}
	for _, n := range []string{"Makefile", "makefile", "GNUmakefile"} {
		if _, err := os.Stat(filepath.Join(top, n)); err == nil {
			return true
		}
	}
	return false
}

func shellCheck(dir, cmdline string) error {
	cmd := sysx.ShellCommand(context.Background(), cmdline)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd.Run()
}
