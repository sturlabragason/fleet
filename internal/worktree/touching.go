package worktree

import (
	"context"
	"fmt"
	"strings"
)

// Touching is `fleet touching` and fleet_touching: the other worktrees of
// dir's repo with the files they change, ignoring ignore. With files
// (relative to cwd, or repo-relative) only the worktrees that change one of
// them, with just those files; overlap reports that some do.
// ctx bounds every git call.
func Touching(ctx context.Context, dir, cwd string, files, ignore []string) (ws []Worktree, overlap bool, err error) {
	if ws, err = others(ctx, dir, ignore...); err != nil {
		return nil, false, err
	}
	if len(files) == 0 {
		return ws, false, nil
	}
	top, err := topLevel(ctx, dir)
	if err != nil {
		return nil, false, err
	}
	rel := make([]string, len(files))
	for i, f := range files {
		rel[i] = RepoRel(top, cwd, f)
	}
	ws = Overlap(ws, rel)
	return ws, len(ws) > 0, nil
}

// TouchingText is Touching's answer as text; withFiles says files were
// asked about.
func TouchingText(ws []Worktree, withFiles bool) string {
	if withFiles && len(ws) == 0 {
		return "no other worktree touches these files\n"
	}
	var b strings.Builder
	for _, w := range ws {
		fmt.Fprintf(&b, "%s  %s  %d files\n", w.Path, w.Branch, len(w.Files))
		for _, f := range w.Files {
			fmt.Fprintf(&b, "    %s\n", f)
		}
	}
	return b.String()
}
