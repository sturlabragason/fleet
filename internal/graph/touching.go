package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sturlabragason/fleet/internal/worktree"
)

// Touching is `fleet touching --graph` and fleet_touching graph=true
// (ADR-136): the other worktrees of dir's repo whose changes overlap files,
// or are one code dependency away from them. files are relative to cwd;
// without files it checks the worktree's own changes. ignore (absolute
// paths, agents' result files) is left out of every worktree's changes.
// use and auto (fleet chose the graph itself) go to the read log (ADR-165).
func Touching(ctx context.Context, home, dir, cwd string, files, ignore []string, use string, auto bool) ([]Coupling, error) {
	_, out, ok := Find(home, dir)
	if !ok {
		return nil, ErrNoGraph
	}
	d, err := LoadDeps(out)
	if err != nil {
		return nil, err
	}
	top, err := worktree.TopLevel(dir)
	if err != nil {
		return nil, err
	}
	mine := make([]string, len(files))
	for i, f := range files {
		mine[i] = worktree.RepoRel(top, cwd, f)
	}
	if len(files) == 0 {
		if mine, err = worktree.Changes(top, ignore...); err != nil {
			return nil, fmt.Errorf("this worktree's changes: %w", err)
		}
	}
	ws, _, _, err := worktree.Touching(ctx, dir, cwd, nil, ignore, false)
	if err != nil {
		return nil, err
	}
	cs := Couple(d, mine, ws)
	Record(home, use, auto, len(cs))
	return cs, nil
}

// Auto says whether a touching check uses the graph (ADR-165). asked is the
// --graph flag or graph parameter, nil when not given: then the graph is
// used for a check of files when it is fresh. note says why a stale one
// was left out; no graph at all is silent.
func Auto(ctx context.Context, home, dir string, files []string, asked *bool) (use bool, note string) {
	if asked != nil {
		return *asked, ""
	}
	if len(files) == 0 {
		return false, ""
	}
	if _, err := Fresh(ctx, home, dir); err != nil {
		if errors.Is(err, ErrStale) {
			return false, err.Error() + "; file overlap only"
		}
		return false, ""
	}
	return true, ""
}

// MergeSteps are the coupling lines of `fleet merge --dry-run`: one per
// other worktree that changes this branch's files or code one dependency
// away.
func MergeSteps(cs []Coupling) []string {
	var steps []string
	for _, c := range cs {
		var what []string
		if len(c.Overlap) > 0 {
			what = append(what, fmt.Sprintf("both change %d file(s)", len(c.Overlap)))
		}
		if n := len(c.Uses) + len(c.UsedBy); n > 0 {
			what = append(what, fmt.Sprintf("%d code link(s)", n))
		}
		order := c.Order
		if order == "" {
			order = "settle the order with its owner (fleet touching --graph shows the files)"
		}
		steps = append(steps, fmt.Sprintf("code graph: %s %s; %s", c.Branch, strings.Join(what, ", "), order))
	}
	return steps
}

// Overlaps reports whether any coupling shares a changed file, the exit code
// of `fleet touching --graph`.
func Overlaps(cs []Coupling) bool {
	for _, c := range cs {
		if c.Overlap != nil {
			return true
		}
	}
	return false
}
