package worktree

import (
	"context"
	"fmt"
	"strings"
)

// Touching is `fleet touching` and fleet_touching: the other worktrees of
// dir's repo with the files they change, ignoring ignore. With files
// (relative to cwd, or repo-relative) only the worktrees that change one of
// them, with just those files; overlap reports that some do. Unless all,
// worktrees whose work is in main and whose only uncommitted changes are
// untracked result files are left out and counted in left (ADR-132).
// ctx bounds the git calls that read changes.
func Touching(ctx context.Context, dir, cwd string, files, ignore []string, all bool) (ws []Worktree, overlap bool, left int, err error) {
	if ws, left, err = others(ctx, dir, all, ignore...); err != nil {
		return nil, false, 0, err
	}
	if len(files) == 0 {
		return ws, false, left, nil
	}
	top, err := topLevel(ctx, dir)
	if err != nil {
		return nil, false, 0, err
	}
	rel := make([]string, len(files))
	for i, f := range files {
		rel[i] = RepoRel(top, cwd, f)
	}
	ws = Overlap(ws, rel)
	return ws, len(ws) > 0, left, nil
}

// TouchingText is Touching's answer as text; withFiles says files were
// asked about, left is how many merged worktrees were left out.
func TouchingText(ws []Worktree, withFiles bool, left int) string {
	var b strings.Builder
	if withFiles && len(ws) == 0 {
		b.WriteString("no other worktree touches these files\n")
	}
	for _, w := range ws {
		fmt.Fprintf(&b, "%s  %s  %d files\n", w.Path, w.Branch, len(w.Files))
		for _, f := range w.Files {
			fmt.Fprintf(&b, "    %s\n", f)
		}
	}
	if left > 0 {
		fmt.Fprintf(&b, "%d merged worktrees left out; fleet worktrees --prune removes them\n", left)
	}
	return b.String()
}
