package fleet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/worktree"
)

// maxBaselineDirty caps how many dirty paths a baseline records; a bigger
// mess records no baseline and auto-close falls back to the strict rule.
const maxBaselineDirty = 2000

// gitOut runs git in dir with GitTimeout and no optional locks.
func gitOut(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), GitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	return string(out), err
}

// Baseline records dir's git state before an agent starts (DESIGN §6.7):
// HEAD, a fingerprint of every dirty path and the linked worktrees. nil when
// dir is not a git work tree or the state can't be read.
func Baseline(dir string) *board.GitBase {
	if dir == "" {
		return nil
	}
	if out, err := gitOut(dir, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		return nil
	}
	head, _ := gitOut(dir, "rev-parse", "--verify", "--quiet", "HEAD")
	dirty, ok := dirtyPrints(dir)
	if !ok {
		return nil
	}
	return &board.GitBase{Head: strings.TrimSpace(head), Dirty: dirty, Worktrees: worktrees(dir)}
}

// dirtyPrints maps each path in git status (untracked files listed one by
// one) to a fingerprint of its content, so a later edit to an already dirty
// file still shows.
func dirtyPrints(dir string) (map[string]string, bool) {
	top, err := gitOut(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, false
	}
	top = strings.TrimSpace(top)
	out, err := gitOut(dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, false
	}
	m := map[string]string{}
	recs := strings.Split(out, "\x00")
	for i := 0; i < len(recs); i++ {
		r := recs[i]
		if len(r) < 4 {
			continue
		}
		code, path := r[:2], r[3:]
		if code[0] == 'R' || code[0] == 'C' {
			i++ // -z puts the rename source in the next record
		}
		if len(m) >= maxBaselineDirty {
			return nil, false
		}
		m[path] = code + " " + fingerprint(filepath.Join(top, path))
	}
	return m, true
}

// fingerprint is an FNV-64 of a regular file's content, "gone" for a missing
// path, and size+mtime for anything else or very large files.
func fingerprint(path string) string {
	st, err := os.Lstat(path)
	if err != nil {
		return "gone"
	}
	if !st.Mode().IsRegular() || st.Size() > 64<<20 {
		return fmt.Sprintf("%s:%d:%d", st.Mode().Type(), st.Size(), st.ModTime().UnixNano())
	}
	fh, err := os.Open(path)
	if err != nil {
		return "unreadable"
	}
	defer fh.Close()
	h := fnv.New64a()
	io.Copy(h, fh)
	return strconv.FormatUint(h.Sum64(), 16)
}

// worktrees lists the repo's linked worktree paths, sorted.
func worktrees(dir string) []string {
	out, err := gitOut(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	var ws []string
	for _, l := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(l, "worktree "); ok {
			ws = append(ws, p)
		}
	}
	sort.Strings(ws)
	return ws
}

// AgentBlocker returns why a done agent can't be auto-closed ("" = it can).
// A dir that git says is not a work tree, or that is gone, has nothing to
// lose; a git error or timeout blocks. With a spawn baseline only the
// agent's own work counts: paths that became dirty or changed since spawn,
// its own worktree (the one spawn made for it, or one named after it per the
// playbook) left dirty or unmerged, and commits not merged into main (or
// master); dirt that predates
// the agent and an unchanged HEAD are fine. Without a baseline it is the
// strict CloseBlocker rule. The agent's own task-card result file never
// counts (#11).
func AgentBlocker(a *board.Agent) string {
	switch workTree(a.Dir) {
	case treeNo:
		return ""
	case treeUnknown:
		return "git state unreadable"
	}
	own := ownResult(a)
	base := a.Baseline
	if base == nil {
		return CloseBlocker(a.Dir, own...)
	}
	g := GitInfo(a.Dir, own...)
	now, ok := dirtyPrints(a.Dir)
	if !g.Repo || !ok {
		return "git state unreadable"
	}
	skip := worktree.Ignored(a.Dir, own)
	var mine []string
	for p, fp := range now {
		if base.Dirty[p] != fp && !skip[p] {
			mine = append(mine, p)
		}
	}
	if len(mine) > 0 {
		sort.Strings(mine)
		return "uncommitted changes by the agent: " + listShort(mine)
	}
	for _, w := range worktrees(a.Dir) {
		if contains(base.Worktrees, w) || filepath.Base(w) != a.Name {
			continue
		}
		if r := CloseBlocker(w, worktreeResult(a, w)...); r != "" && r != "no main or master branch" {
			return "worktree left behind (" + r + "): " + w
		}
	}
	head, _ := gitOut(a.Dir, "rev-parse", "--verify", "--quiet", "HEAD")
	if strings.TrimSpace(head) == base.Head {
		return ""
	}
	switch {
	case g.Base == "":
		return "no main or master branch"
	case !g.Merged && a.Worktree != nil:
		return "worktree left behind (not merged into " + g.Base + "): " + a.Worktree.Path
	case !g.Merged:
		return "not merged into " + g.Base
	}
	return ""
}

// worktreeResult is the agent's result file inside its own worktree w, for
// an agent whose dir is the main checkout but who wrote the file where it
// worked.
func worktreeResult(a *board.Agent, w string) []string {
	if a.Card == nil || a.Card.Result == "" || filepath.IsAbs(a.Card.Result) {
		return nil
	}
	return []string{filepath.Join(w, a.Card.Result)}
}

type treeState int

const (
	treeYes treeState = iota
	treeNo
	treeUnknown
)

// workTree asks git whether dir is inside a work tree. Only a clear "not a
// git repository" (or a missing dir) is treeNo; a timeout or any other git
// error is treeUnknown.
func workTree(dir string) treeState {
	if dir == "" {
		return treeNo
	}
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return treeNo
	}
	ctx, cancel := context.WithTimeout(context.Background(), GitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	switch {
	case err == nil && strings.TrimSpace(string(out)) == "true":
		return treeYes
	case err == nil: // inside .git or a bare repo
		return treeNo
	case ctx.Err() == nil && strings.Contains(stderr.String(), "not a git repository"):
		return treeNo
	}
	return treeUnknown
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// listShort joins up to three items and counts the rest.
func listShort(xs []string) string {
	var b bytes.Buffer
	for i, x := range xs {
		if i == 3 {
			fmt.Fprintf(&b, " (+%d more)", len(xs)-3)
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(x)
	}
	return b.String()
}
