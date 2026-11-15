package worktree

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
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
	Merged *bool  `json:"merged"` // the work is in main (Merged, ADR-132); nil: unknown
	Dirty  bool   `json:"dirty"`  // git status shows anything, untracked included
	// ResultOnly: the only changes are untracked result files (Results), the
	// agent's handoff. Still dirty, but prunable once merged: Prune archives
	// them first (ADR-132).
	ResultOnly bool     `json:"result_only,omitempty"`
	Results    []string `json:"results,omitempty"` // worktree-relative
	Agent      string   `json:"agent"`             // "live", "parked", "closed" (resumable) or "": the agent in it
	Prunable   bool     `json:"prunable"`
	Gone       bool     `json:"gone"` // the dir was deleted by hand; only the registration is left
	Unknown    bool     `json:"-"`    // a git query failed; never prunable
}

// Stale lists the repo's linked worktrees under `<main>.wt/`. agents maps a
// worktree path to the board state ("live", "parked" or "closed") of the
// agent that holds it; a live or parked one is never prunable, a closed one
// is once its work is in main. results are the absolute task-card result
// files of the agents on the board and in the closed records; they, and any
// `result*.md` at a worktree's root, are a handoff, not work left behind.
// cwd's own worktree is never prunable.
func Stale(main, cwd string, agents map[string]string, results ...string) ([]Entry, error) {
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
	cards := map[string]bool{}
	for _, p := range results {
		if filepath.IsAbs(p) { // the file may not exist: resolve its directory, keep its name
			cards[filepath.Join(realPath(filepath.Dir(p)), filepath.Base(p))] = true
		}
	}
	cwd = realPath(cwd)
	var es []Entry
	var heads []string
	var at []int // es index of each head
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
		untracked, other, failed := status(path)
		e.Dirty, e.Unknown = other || len(untracked) > 0, failed
		if !other && len(untracked) > 0 && allResults(path, untracked, cards) {
			e.ResultOnly, e.Results = true, untracked
		}
		if w.Head != "" {
			heads, at = append(heads, w.Head), append(at, len(es))
		}
		es = append(es, e)
	}
	if len(Bases(main)) > 0 {
		for j, m := range MergedAll(main, heads) {
			es[at[j]].Merged = &m
		}
	}
	for i := range es {
		e := &es[i]
		if e.Gone {
			continue
		}
		if e.Merged == nil {
			e.Unknown = true
		}
		inside := cwd == e.Path || strings.HasPrefix(cwd, e.Path+string(filepath.Separator))
		free := e.Agent == "" || e.Agent == "closed"
		e.Prunable = !e.Unknown && *e.Merged && (!e.Dirty || e.ResultOnly) && free && !inside
	}
	return es, nil
}

// allResults reports whether every worktree-relative file is a result file:
// one in cards (absolute), or a `result*.md` at the worktree root.
func allResults(wt string, files []string, cards map[string]bool) bool {
	for _, f := range files {
		root, _ := filepath.Match("result*.md", f)
		if !root && !cards[filepath.Join(wt, filepath.FromSlash(f))] {
			return false
		}
	}
	return true
}

// status reads dir's git status: the untracked files, whether anything else
// (a modified, staged or deleted tracked file) shows, and whether git could
// not say.
func status(dir string) (untracked []string, other, failed bool) {
	ctx, cancel := context.WithTimeout(context.Background(), staleTimeout)
	defer cancel()
	out, err := gitCtx(ctx, dir, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, true, true
	}
	for _, r := range strings.Split(out, "\x00") {
		switch {
		case r == "":
		case strings.HasPrefix(r, "?? "):
			untracked = append(untracked, r[3:])
		default: // a rename's source record lands here too: it is dirt either way
			other = true
		}
	}
	return untracked, other, false
}

// dirSize sums the file sizes under dir without following symlinks.
func dirSize(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // skip what cannot be read; the size is a best-effort sum
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
	Entry    Entry
	Removed  bool
	Branch   string   // the fleet/ branch deleted with it
	Archived []string // where its result files were copied
	Err      error    // why git refused
}

// Prune removes each prunable entry: `git worktree remove` without --force,
// so git itself refuses a tree with uncommitted or untracked files, then
// deletes its fleet/ branch (`-D` only when Merged says its work is in main
// by patch). A tree whose only change is its result files has them copied
// to <fleet home>/results/<repo>/<worktree>/ first (ADR-132). A gone entry
// only has its registration pruned. One failure never stops the rest.
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
			untracked, other, failed := status(e.Path)
			if failed || other || !subset(untracked, e.Results) {
				r.Err = fmt.Errorf("uncommitted changes appeared")
				break
			}
			if r.Archived, r.Err = archive(main, e.Path, untracked); r.Err != nil {
				break
			}
			if _, r.Err = gitCtx(ctx, main, "worktree", "remove", e.Path); r.Err != nil {
				break
			}
			r.Removed = true
			if strings.HasPrefix(e.Branch, BranchPrefix) {
				_, err := gitCtx(ctx, main, "branch", "-d", e.Branch)
				if err != nil && Merged(main, e.Branch) {
					_, err = gitCtx(ctx, main, "branch", "-D", e.Branch)
				}
				if err == nil {
					r.Branch = e.Branch
				}
			}
		}
		cancel()
		rs = append(rs, r)
	}
	return rs
}

func subset(xs, of []string) bool {
	for _, x := range xs {
		if !slices.Contains(of, x) {
			return false
		}
	}
	return true
}

// archive moves wt's result files to <fleet home>/results/<repo>/<wt name>/,
// keeping each earlier copy: a taken name gets -1, -2, ... before its
// extension. It returns the new paths.
func archive(main, wt string, files []string) ([]string, error) {
	dir := filepath.Join(logx.Home(), "results", filepath.Base(main), filepath.Base(wt))
	var done []string
	for _, f := range files {
		src := filepath.Join(wt, filepath.FromSlash(f))
		if st, err := os.Lstat(src); err != nil || !st.Mode().IsRegular() {
			return done, fmt.Errorf("%s is not a regular file", f)
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return done, err
		}
		dst := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return done, err
		}
		ext := filepath.Ext(dst)
		stem := strings.TrimSuffix(dst, ext)
		for n := 1; ; n++ {
			out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if errors.Is(err, fs.ErrExist) {
				dst = fmt.Sprintf("%s-%d%s", stem, n, ext)
				continue
			}
			if err != nil {
				return done, err
			}
			_, err = out.Write(data)
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return done, err
			}
			break
		}
		if err := os.Remove(src); err != nil {
			return done, err
		}
		done = append(done, dst)
	}
	return done, nil
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
