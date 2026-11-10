package worktree

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Remote is the remote fleet pushes agent branches to for CI (ADR-69).
const Remote = "origin"

// remoteTimeout bounds one network git call (fetch, push) on a branch sweep.
const remoteTimeout = 20 * time.Second

// RemoteBranch is one fleet/ branch on the remote (`fleet branches`, ADR-85).
type RemoteBranch struct {
	Branch   string `json:"branch"` // fleet/<name>, without the remote
	Sha      string `json:"sha"`
	Merged   bool   `json:"merged"` // every commit is in main, by ancestry or by patch
	Agent    string `json:"agent"`  // "live", "parked" or ""
	Prunable bool   `json:"prunable"`
}

// Merged reports whether every commit of rev is in the local main (or
// master) or its remote-tracking copy, either as an ancestor or as an equal
// patch: `fleet merge` rebases before it merges, so the pushed copy of a
// merged branch is usually not an ancestor of main (ADR-85). Any git error
// reads as not merged.
func Merged(dir, rev string) bool {
	base := baseBranch(dir)
	if base == "" {
		return false
	}
	bases := []string{base}
	if _, err := git(dir, "rev-parse", "--verify", "--quiet", "refs/remotes/"+Remote+"/"+base); err == nil {
		bases = append(bases, "refs/remotes/"+Remote+"/"+base)
	}
	for _, b := range bases {
		ctx, cancel := context.WithTimeout(context.Background(), staleTimeout)
		out, err := gitCtx(ctx, dir, "cherry", b, rev)
		cancel()
		if err == nil && !strings.Contains("\n"+out, "\n+") {
			return true
		}
	}
	return false
}

// RemoteBranches fetches the remote with --prune and lists its fleet/
// branches. agents maps a branch to the board state ("live" or "parked") of
// the agent on it; those are never prunable.
func RemoteBranches(main string, agents map[string]string) ([]RemoteBranch, error) {
	ctx, cancel := context.WithTimeout(context.Background(), remoteTimeout)
	_, err := gitCtx(ctx, main, "fetch", "--quiet", "--prune", Remote)
	cancel()
	if err != nil {
		return nil, err
	}
	out, err := git(main, "for-each-ref", "--format=%(refname) %(objectname)", "refs/remotes/"+Remote+"/"+BranchPrefix)
	if err != nil {
		return nil, err
	}
	var bs []RemoteBranch
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		ref, sha, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		b := RemoteBranch{Branch: strings.TrimPrefix(ref, "refs/remotes/"+Remote+"/"), Sha: sha}
		b.Agent = agents[b.Branch]
		b.Merged = Merged(main, sha)
		b.Prunable = b.Merged && b.Agent == ""
		bs = append(bs, b)
	}
	return bs, nil
}

// PruneRemote deletes the prunable branches from the remote in one push.
// Each delete carries a lease on the sha that was checked, so a branch that
// moved since is refused, not lost. It returns the branches deleted.
func PruneRemote(main string, bs []RemoteBranch) ([]string, error) {
	args := []string{"push", "--porcelain", Remote}
	var specs, names []string
	for _, b := range bs {
		if !b.Prunable {
			continue
		}
		args = append(args, "--force-with-lease=refs/heads/"+b.Branch+":"+b.Sha)
		specs = append(specs, ":refs/heads/"+b.Branch)
		names = append(names, b.Branch)
	}
	if len(specs) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), remoteTimeout)
	defer cancel()
	out, err := gitKeep(ctx, main, append(args, specs...)...)
	// --porcelain reports each ref; with a refused lease git still deletes
	// the others and exits non-zero, so read what happened per ref.
	var done []string
	for _, n := range names {
		if strings.Contains(out, "-\t:refs/heads/"+n+"\t[deleted]") {
			done = append(done, n)
		}
	}
	if err != nil && len(done) < len(names) {
		return done, fmt.Errorf("%d of %d not deleted: %w", len(names)-len(done), len(names), err)
	}
	return done, nil
}

// DeleteMergedRemote deletes branch from the remote when it is merged into
// main (Merged) and was pushed: the closing agent's safety net (ADR-85). A
// branch with no remote-tracking ref was never pushed from here and costs no
// network call. It reports whether the branch was deleted.
func DeleteMergedRemote(dir, branch string) (bool, error) {
	if !strings.HasPrefix(branch, BranchPrefix) {
		return false, nil
	}
	tracking := "refs/remotes/" + Remote + "/" + branch
	if _, err := git(dir, "rev-parse", "--verify", "--quiet", tracking); err != nil {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), remoteTimeout)
	_, err := gitCtx(ctx, dir, "fetch", "--quiet", Remote, "+refs/heads/"+branch+":"+tracking)
	cancel()
	if err != nil {
		// Gone from the remote already (a GitHub auto-delete, a sweep):
		// drop the stale tracking ref and stop.
		ctx, cancel := context.WithTimeout(context.Background(), remoteTimeout)
		out, lerr := gitCtx(ctx, dir, "ls-remote", "--heads", Remote, "refs/heads/"+branch)
		cancel()
		if lerr == nil && strings.TrimSpace(out) == "" {
			git(dir, "update-ref", "-d", tracking)
			return false, nil
		}
		return false, err
	}
	sha, err := git(dir, "rev-parse", tracking)
	if err != nil {
		return false, err
	}
	b := RemoteBranch{Branch: branch, Sha: strings.TrimSpace(sha), Prunable: true}
	if b.Merged = Merged(dir, b.Sha); !b.Merged {
		return false, nil
	}
	done, err := PruneRemote(dir, []RemoteBranch{b})
	return len(done) == 1, err
}
