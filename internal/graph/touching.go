package graph

import (
	"context"
	"errors"
	"fmt"

	"github.com/sturlabragason/fleet/internal/worktree"
)

// Touching is `fleet touching --graph` and fleet_touching graph=true
// (ADR-136): the other worktrees of dir's repo whose changes overlap files,
// or are one code dependency away from them. files are relative to cwd;
// without files it checks the worktree's own changes. ignore (absolute
// paths, agents' result files) is left out of every worktree's changes.
func Touching(ctx context.Context, home, dir, cwd string, files, ignore []string) ([]Coupling, error) {
	_, out, ok := Find(home, dir)
	if !ok {
		return nil, errors.New("no code graph for this repo; run fleet graph")
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
	return Couple(d, mine, ws), nil
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
