package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
	"github.com/sturlabragason/fleet/internal/graph"
	"github.com/sturlabragason/fleet/internal/logx"
)

// cmdGraph builds or locates the repo's code graph (DESIGN §6.15, ADR-128).
func cmdGraph(args []string) error {
	fs := flag.NewFlagSet("graph", flag.ContinueOnError)
	dir := fs.String("C", ".", "directory inside the repo")
	force := fs.Bool("force", false, "rebuild everything, not just what changed")
	path := fs.Bool("path", false, "print the graph's directory (built or not) and stop")
	asJSON := fs.Bool("json", false, "print the stamp of the built graph as JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return errors.New("usage: fleet graph [-C dir] [--force] [--path] [--json]")
	}
	root, err := graph.Root(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fleet graph:", err)
		return exitCode(1)
	}
	home := logx.Home()
	if *path {
		fmt.Println(graph.Dir(home, root))
		return nil
	}
	start := time.Now()
	st, err := graph.Build(context.Background(), home, root, *force)
	switch {
	case errors.Is(err, graph.ErrBusy):
		fmt.Println("a build is running; it will run again when it ends")
		return nil
	case err != nil:
		fmt.Fprintln(os.Stderr, "fleet graph:", err)
		var ce *companion.Error
		if errors.As(err, &ce) && ce.Spec.Hint != "" {
			fmt.Fprintln(os.Stderr, "fleet graph: to get it:", ce.Spec.Hint)
		}
		return exitCode(1)
	}
	if *asJSON {
		b, _ := json.MarshalIndent(st, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	commit := st.Commit
	if len(commit) > 7 {
		commit = commit[:7]
	}
	fmt.Printf("graph of %s: %d nodes, %d edges, from %s, in %s (%.1fs)\n",
		st.Root, st.Nodes, st.Edges, commit, graph.Dir(home, root), time.Since(start).Seconds())
	return nil
}
