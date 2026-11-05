package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BranchPrefix is the branch namespace spawn gives an agent (DESIGN §4).
const BranchPrefix = "fleet/"

// CreateTimeout bounds `git worktree add`, which checks the tree out and so
// sits on the synchronous spawn path (DESIGN §4). Past it spawn treats git as
// having refused and runs the agent in dir instead.
const CreateTimeout = 30 * time.Second

// probeTimeout bounds the read-only git questions Create and Of ask.
const probeTimeout = 2 * time.Second

// ErrNoRepo says dir is not inside a git repo, so there is nothing to add a
// worktree to. Spawn treats it as "no worktree here", not as a failure.
var ErrNoRepo = errors.New("not in a git repo")

// Agent is the worktree an agent works in.
type Agent struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Repo   string `json:"repo,omitempty"` // main worktree it was made from
}

// Main returns the repo's main worktree: the first entry of
// `git worktree list`, so a linked worktree still points at the checkout the
// agent dirs hang off. ErrNoRepo when dir is not in a repo or is bare.
func Main(dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("no dir: %w", ErrNoRepo)
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	out, err := gitCtx(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("%s: %w", dir, ErrNoRepo)
	}
	ws := Parse(out)
	if len(ws) == 0 || ws[0].Bare {
		return "", fmt.Errorf("%s: %w", dir, ErrNoRepo)
	}
	return realPath(ws[0].Path), nil
}

// Path is where an agent's worktree goes: `<repo>.wt/<name>` beside the
// repo's main worktree, the place the worker playbook names.
func Path(main, name string) string {
	return filepath.Join(filepath.Dir(main), filepath.Base(main)+".wt", name)
}

// Create makes the worktree for an agent spawned in dir: `<repo>.wt/<name>`
// on branch `fleet/<name>`, branched off dir's HEAD. A live worktree at that
// path, from an earlier agent of the same name, is reused as is; a stale
// registration whose dir is gone is pruned and made again. It returns
// ErrNoRepo when dir is not in a git repo, and an error when git refuses or
// takes longer than CreateTimeout (no commit yet, the branch is checked out
// elsewhere, the path is taken by something else).
func Create(dir, name string) (*Agent, error) {
	main, err := Main(dir)
	if err != nil {
		return nil, err
	}
	path, branch := Path(main, name), BranchPrefix+name
	if w, ok := at(main, path); ok {
		return &Agent{Path: realPath(w.Path), Branch: w.Branch, Repo: main}, nil
	}
	if _, err := os.Lstat(path); err == nil {
		return nil, fmt.Errorf("%s exists and is not a worktree of %s", path, main)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), CreateTimeout)
	defer cancel()
	// Drop any registration left by a worktree dir someone deleted by hand,
	// so `worktree add` can take the path and the branch back.
	_, _ = gitCtx(ctx, dir, "worktree", "prune")
	args := []string{"worktree", "add", "-b", branch, path}
	if _, err := gitCtx(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		args = []string{"worktree", "add", path, branch} // the agent's own branch is already there
	}
	if _, err := gitCtx(ctx, dir, args...); err != nil {
		return nil, err
	}
	return &Agent{Path: realPath(path), Branch: branch, Repo: main}, nil
}

// Of returns the linked worktree dir sits in, or nil when dir is in the
// repo's main worktree or in no repo at all. Adopt uses it: a session that
// was already started in a worktree keeps it on the board, so close and
// sweep can report the work left there.
func Of(dir string) *Agent {
	main, err := Main(dir)
	if err != nil {
		return nil
	}
	top, err := TopLevel(dir)
	if err != nil || top == main {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	br, _ := gitCtx(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	return &Agent{Path: top, Branch: strings.TrimSpace(br), Repo: main}
}

// at finds the live worktree registered at path, comparing resolved paths. A
// prunable entry — git still lists a worktree whose dir someone deleted —
// does not count, so Create remakes it rather than starting an agent in a
// directory that is not there.
func at(dir, path string) (Worktree, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	out, err := gitCtx(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return Worktree{}, false
	}
	want := realPath(path)
	for _, w := range Parse(out) {
		if w.Prunable || realPath(w.Path) != want {
			continue
		}
		if _, err := os.Stat(w.Path); err != nil {
			continue
		}
		return w, true
	}
	return Worktree{}, false
}
