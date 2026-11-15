package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/graph"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/playbook"
	"github.com/sturlabragason/fleet/internal/worktree"
)

// exitCode makes run end the process with this status and no error text.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

// cmdPlaybook prints the worker playbook text for a directory at one effort
// level (ADR-57). The level is the flag's, not the one a spawn into that dir
// would pick: --effort shows any of them, and the default is pro.
func cmdPlaybook(args []string) error {
	fs := flag.NewFlagSet("playbook", flag.ContinueOnError)
	effort := fs.String("effort", string(playbook.Pro), "effort level: light, standard or pro")
	if err := fs.Parse(args); err != nil {
		return err
	}
	lvl, err := playbook.ParseLevel(*effort)
	if err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) > 1 {
		return fmt.Errorf("usage: fleet playbook [--effort light|standard|pro] [dir]")
	}
	dir := "."
	if len(rest) == 1 {
		dir = rest[0]
	}
	text, src := playbook.Resolve(lvl, logx.Home(), dir)
	fmt.Fprintf(os.Stderr, "# sources: %s\n", strings.Join(src, ", "))
	fmt.Print(text)
	if text != "" && !strings.HasSuffix(text, "\n") {
		fmt.Println()
	}
	return nil
}

// resultFiles are the task-card result files of every agent on the board.
func resultFiles() []string { return fleet.ResultFiles(board.NewStore(logx.Home()).Load()) }

// cmdTouching lists the files other worktrees of this repo change; with file
// arguments it reports only overlaps. Exit 0: no overlap, 1: overlap, 2: error
// (so a failed check never reads as all-clear).
func cmdTouching(args []string) error {
	code, err := touching(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fleet touching:", err)
		return exitCode(2)
	}
	if code != 0 {
		return exitCode(code)
	}
	return nil
}

func touching(args []string) (int, error) {
	fs := flag.NewFlagSet("touching", flag.ContinueOnError)
	dir := fs.String("C", ".", "directory inside the repo")
	asJSON := fs.Bool("json", false, "JSON output")
	all := fs.Bool("all", false, "also list worktrees whose work is already in main")
	withGraph := fs.Bool("graph", false, "also code dependencies between the files, from the code graph")
	if err := fs.Parse(args); err != nil {
		return 0, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return 0, err
	}
	files := fs.Args()
	if *withGraph {
		return touchingGraph(context.Background(), *dir, cwd, files, *asJSON)
	}
	ws, overlap, left, err := worktree.Touching(context.Background(), *dir, cwd, files, resultFiles(), *all)
	if err != nil {
		return 0, err
	}
	if *asJSON {
		if ws == nil {
			ws = []worktree.Worktree{}
		}
		b, _ := json.MarshalIndent(ws, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Print(worktree.TouchingText(ws, len(files) > 0, left))
	}
	if overlap {
		return 1, nil
	}
	return 0, nil
}

// touchingGraph is `fleet touching --graph` (ADR-136): the other worktrees
// whose changes overlap the files, or are one code dependency away from
// them, with the merge order that follows. Without files it checks this
// worktree's own changes. The exit code stays the file-overlap answer.
func touchingGraph(ctx context.Context, dir, cwd string, files []string, asJSON bool) (int, error) {
	cs, err := graph.Touching(ctx, logx.Home(), dir, cwd, files, resultFiles())
	if err != nil {
		return 0, err
	}
	if asJSON {
		if cs == nil {
			cs = []graph.Coupling{}
		}
		b, _ := json.MarshalIndent(cs, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Print(graph.CouplingText(cs))
	}
	if graph.Overlaps(cs) {
		return 1, nil
	}
	return 0, nil
}
