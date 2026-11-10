package worktree

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// staleTimeout bounds each git question `fleet worktrees` asks.
const staleTimeout = 3 * time.Second

// Entry is one linked worktree under `<repo>.wt/`: what is left of an agent's
// work, how big it is and whether it can go (`fleet worktrees`, ADR-79).
type Entry struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Bytes  int64  `json:"bytes"`
	Merged *bool  `json:"merged"` // HEAD is in main or master; nil: unknown
	Dirty  bool   `json:"dirty"`  // git status shows anything, untracked included
	// ResultOnly: the only change is the agent's untracked result.md handoff.
	// Still dirty, so never pruned: removing it would lose the result.
	ResultOnly bool   `json:"result_only,omitempty"`
	Agent      string `json:"agent"` // "live", "parked" or "": the board state of an agent in it
	Prunable   bool   `json:"prunable"`
	Gone       bool   `json:"gone"` // the dir was deleted by hand; only the registration is left
	Unknown    bool   `json:"-"`    // a git query failed; never prunable
}

// Stale lists the repo's linked worktrees under `<main>.wt/`. agents maps a
// worktree path to the board state ("live" or "parked") of the agent that
// holds it; cwd's own worktree is never prunable.
func Stale(main, cwd string, agents map[string]string) ([]Entry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), staleTimeout)
	out, err := gitCtx(ctx, main, "worktree", "list", "--porcelain")
	cancel()
	if err != nil {
		return nil, err
	}
	root := filepath.Dir(Path(main, "x")) + string(filepath.Separator)
	held := map[string]string{}
	for p, s := range agents {
		held[realPath(p)] = s
	}
	cwd = realPath(cwd)
	base := baseBranch(main)
	var es []Entry
	for i, w := range Parse(out) {
		path := realPath(w.Path)
		if i == 0 || w.Bare || !strings.HasPrefix(path, root) {
			continue
		}
		e := Entry{Name: filepath.Base(path), Path: path, Branch: w.Branch, Agent: held[path]}
		if _, err := os.Stat(w.Path); err != nil {
			e.Gone, e.Prunable = true, true
			es = append(es, e)
			continue
		}
		e.Bytes = dirSize(path)
		e.Dirty, e.Unknown, e.ResultOnly = status(path)
		if base != "" {
			ctx, cancel := context.WithTimeout(context.Background(), staleTimeout)
			_, err := gitCtx(ctx, path, "merge-base", "--is-ancestor", "HEAD", base)
			cancel()
			m := err == nil
			e.Merged = &m
		}
		if e.Merged == nil {
			e.Unknown = true
		}
		inside := cwd == path || strings.HasPrefix(cwd, path+string(filepath.Separator))
		e.Prunable = !e.Unknown && e.Merged != nil && *e.Merged && !e.Dirty && e.Agent == "" && !inside
		es = append(es, e)
	}
	return es, nil
}

// isDirty reports whether dir has anything in git status; failed is true when
// git could not say.
func isDirty(dir string) (dirty, failed bool) {
	dirty, failed, _ = status(dir)
	return dirty, failed
}

// status is isDirty plus whether an untracked result.md is the only change.
func status(dir string) (dirty, failed, resultOnly bool) {
	ctx, cancel := context.WithTimeout(context.Background(), staleTimeout)
	defer cancel()
	out, err := gitCtx(ctx, dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return true, true, false
	}
	out = strings.TrimSpace(out)
	return out != "", false, out == "?? result.md"
}

// dirSize sums the file sizes under dir without following symlinks.
func dirSize(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			n += info.Size()
		}
		return nil
	})
	return n
}

// PruneResult is the outcome for one entry of Prune.
type PruneResult struct {
	Entry   Entry
	Removed bool
	Branch  string // the fleet/ branch deleted with it
	Err     error  // why git refused
}

// Prune removes each prunable entry: `git worktree remove` without --force,
// so git itself refuses a tree with uncommitted or untracked files, then
// `git branch -d` for its fleet/ branch. A gone entry only has its
// registration pruned. One failure never stops the rest.
func Prune(main string, es []Entry) []PruneResult {
	var rs []PruneResult
	pruned := false
	for _, e := range es {
		if !e.Prunable {
			continue
		}
		r := PruneResult{Entry: e}
		ctx, cancel := context.WithTimeout(context.Background(), CreateTimeout)
		switch {
		case e.Gone:
			if !pruned {
				_, r.Err = gitCtx(ctx, main, "worktree", "prune")
				pruned = r.Err == nil
			}
			r.Removed = r.Err == nil
		default:
			if dirty, _ := isDirty(e.Path); dirty {
				r.Err = fmt.Errorf("uncommitted changes appeared")
				break
			}
			if _, r.Err = gitCtx(ctx, main, "worktree", "remove", e.Path); r.Err != nil {
				break
			}
			r.Removed = true
			if strings.HasPrefix(e.Branch, BranchPrefix) {
				if _, err := gitCtx(ctx, main, "branch", "-d", e.Branch); err == nil {
					r.Branch = e.Branch
				}
			}
		}
		cancel()
		rs = append(rs, r)
	}
	return rs
}

// HumanSize renders a byte count as 412M, 3.1G, 17K.
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	f, suf := float64(n)/unit, "K"
	for _, s := range []string{"M", "G", "T"} {
		if f < unit {
			break
		}
		f, suf = f/unit, s
	}
	if f >= 10 {
		return fmt.Sprintf("%.0f%s", f, suf)
	}
	return fmt.Sprintf("%.1f%s", f, suf)
}
