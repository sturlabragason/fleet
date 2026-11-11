// Package worktree lists the other git worktrees of a repo and the files each
// one changes, so an agent can check for overlap before editing
// (`fleet touching`, DESIGN §4). It runs git, the one external tool it needs.
package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
)

// Worktree is one entry of `git worktree list --porcelain`, plus the files it
// changes once Changes has run.
type Worktree struct {
	Path     string   `json:"path"`
	Branch   string   `json:"branch"` // short name; "(detached)" or "(bare)"
	Head     string   `json:"-"`
	Bare     bool     `json:"-"`
	Prunable bool     `json:"-"`
	Files    []string `json:"files"`
}

// Parse reads `git worktree list --porcelain` output.
func Parse(out string) []Worktree {
	var ws []Worktree
	var cur *Worktree
	for _, line := range strings.Split(out, "\n") {
		key, val, _ := strings.Cut(strings.TrimRight(line, "\r"), " ")
		if key == "worktree" {
			ws = append(ws, Worktree{Path: val})
			cur = &ws[len(ws)-1]
			continue
		}
		if cur == nil {
			continue
		}
		switch key {
		case "HEAD":
			cur.Head = val
		case "branch":
			cur.Branch = strings.TrimPrefix(val, "refs/heads/")
		case "detached":
			cur.Branch = "(detached)"
		case "bare":
			cur.Bare, cur.Branch = true, "(bare)"
		case "prunable":
			cur.Prunable = true
		}
	}
	return ws
}

// ParseStatus reads `git status --porcelain -z` output and returns the paths
// (for a rename or copy, both the new and the original path).
func ParseStatus(out string) []string {
	var files []string
	parts := strings.Split(out, "\x00")
	for i := 0; i < len(parts); i++ {
		e := parts[i]
		if len(e) < 4 {
			continue
		}
		files = append(files, e[3:])
		if e[0] == 'R' || e[0] == 'C' { // with -z the original path follows
			if i+1 < len(parts) && parts[i+1] != "" {
				files = append(files, parts[i+1])
			}
			i++
		}
	}
	return files
}

// Overlap keeps, per worktree, only the files also in want, and drops
// worktrees with no overlap. The input is not modified.
func Overlap(ws []Worktree, want []string) []Worktree {
	set := map[string]bool{}
	for _, f := range want {
		set[filepath.ToSlash(filepath.Clean(f))] = true
	}
	var out []Worktree
	for _, w := range ws {
		var hit []string
		for _, f := range w.Files {
			if set[f] {
				hit = append(hit, f)
			}
		}
		if len(hit) > 0 {
			w.Files = hit
			out = append(out, w)
		}
	}
	return out
}

// RepoRel turns a file argument into a repo-relative path: an absolute path,
// or a path relative to cwd when that exists inside the repo, or else the
// argument itself taken as repo-relative.
func RepoRel(top, cwd, arg string) string {
	p := arg
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, arg)
		if _, err := os.Stat(p); err != nil {
			return filepath.ToSlash(filepath.Clean(arg))
		}
	}
	if r, err := filepath.Rel(realPath(top), realPath(p)); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(filepath.Clean(arg))
}

func git(dir string, args ...string) (string, error) {
	return gitCtx(context.Background(), dir, args...)
}

// gitCtx runs git in dir without inherited GIT_DIR-style variables (a git
// hook or wrapper may set them), which would point every dir at one repo.
func gitCtx(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := gitKeep(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	return out, nil
}

// gitKeep is gitCtx that also returns stdout when git fails, for commands
// that report per item and exit non-zero when one fails (git push).
func gitKeep(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	for _, e := range os.Environ() {
		switch k, _, _ := strings.Cut(e, "="); k {
		case "GIT_DIR", "GIT_COMMON_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE":
		default:
			cmd.Env = append(cmd.Env, e)
		}
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			err = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return string(out), fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// TopLevel returns the root of the worktree that contains dir.
func TopLevel(dir string) (string, error) { return topLevel(context.Background(), dir) }

func topLevel(ctx context.Context, dir string) (string, error) {
	out, err := gitCtx(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("%s is not in a git repo", dir)
	}
	return realPath(strings.TrimSpace(out)), nil
}

// CommonDir returns the git dir shared by every worktree of the repo that
// contains dir, so two dirs in worktrees of one repo give the same path.
// It gives up after 2s: the mail access check calls it under mail.lock.
func CommonDir(dir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := gitCtx(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("%s is not in a git repo", dir)
	}
	return realPath(strings.TrimSpace(out)), nil
}

// Others lists every worktree of the repo containing dir except the one dir
// is in, with the files each changes, skipping bare, prunable and unchanged
// worktrees. It fails closed: if any worktree's changes can't be read, it
// returns an error rather than a list that might wrongly look clear. ignore
// is passed on to Changes.
func Others(dir string, ignore ...string) ([]Worktree, error) {
	return others(context.Background(), dir, ignore...)
}

func others(ctx context.Context, dir string, ignore ...string) ([]Worktree, error) {
	top, err := topLevel(ctx, dir)
	if err != nil {
		return nil, err
	}
	out, err := gitCtx(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var res []Worktree
	for _, w := range Parse(out) {
		if w.Bare || w.Prunable || realPath(w.Path) == top {
			continue
		}
		if w.Files, err = changes(ctx, w.Path, ignore...); err != nil {
			logx.For("worktree").Warn("changes unreadable", "worktree", w.Path, "err", err)
			return nil, fmt.Errorf("worktree %s: %w", w.Path, err)
		}
		if len(w.Files) > 0 {
			res = append(res, w)
		}
	}
	return res, nil
}

// Changes returns the repo-relative files a worktree changes: those differing
// from its merge base with main (or master; skipped if neither exists) plus
// uncommitted ones, including untracked. Renames count as both paths. Any git
// failure is an error, so a caller never mistakes it for "no changes".
// ignore holds absolute paths, such as agents' task-card result files (#11),
// left out of the uncommitted part; a committed change to one still counts,
// since it would conflict on merge.
func Changes(wt string, ignore ...string) ([]string, error) {
	return changes(context.Background(), wt, ignore...)
}

func changes(ctx context.Context, wt string, ignore ...string) ([]string, error) {
	set := map[string]bool{}
	if base := baseBranchCtx(ctx, wt); base != "" {
		mb, err := gitCtx(ctx, wt, "merge-base", "HEAD", base)
		if err != nil {
			return nil, err
		}
		out, err := gitCtx(ctx, wt, "diff", "--name-only", "--no-renames", strings.TrimSpace(mb))
		if err != nil {
			return nil, err
		}
		for _, f := range strings.Split(out, "\n") {
			if f = strings.TrimSpace(f); f != "" {
				set[f] = true
			}
		}
	}
	out, err := gitCtx(ctx, wt, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	skip := Ignored(wt, ignore)
	for _, f := range ParseStatus(out) {
		if !skip[f] {
			set[f] = true
		}
	}
	files := make([]string, 0, len(set))
	for f := range set {
		files = append(files, f)
	}
	sort.Strings(files)
	return files, nil
}

// Ignored maps the absolute paths in ignore that lie inside the work tree
// holding dir to their repo-relative form, as git status prints them.
func Ignored(dir string, ignore []string) map[string]bool {
	if len(ignore) == 0 {
		return nil
	}
	top, err := TopLevel(dir)
	if err != nil {
		return nil
	}
	m := map[string]bool{}
	for _, p := range ignore {
		if p == "" || !filepath.IsAbs(p) {
			continue
		}
		// The file may not exist: resolve its directory, keep its name.
		full := filepath.Join(realPath(filepath.Dir(p)), filepath.Base(p))
		if r, err := filepath.Rel(top, full); err == nil && r != "." && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			m[filepath.ToSlash(r)] = true
		}
	}
	return m
}

// baseBranch returns "main", else "master", else "".
func baseBranch(wt string) string { return baseBranchCtx(context.Background(), wt) }

func baseBranchCtx(ctx context.Context, wt string) string {
	for _, b := range []string{"main", "master"} {
		if _, err := gitCtx(ctx, wt, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil {
			return b
		}
	}
	return ""
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
