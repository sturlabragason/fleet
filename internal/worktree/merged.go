package worktree

import (
	"context"
	"strings"
	"sync"
	"time"
)

// mergedTimeout bounds one MergedAll: a few git calls whose cost grows with
// the commits the revs have beyond main, not with the number of revs.
const mergedTimeout = 30 * time.Second

// Merged is the one answer to "is rev's work in main" (ADR-87, ADR-132):
// every commit rev has beyond the local main (or master), or its
// remote-tracking copy, is in it, either as an ancestor or as an equal patch
// (`git cherry`'s test), which covers a rebase or cherry-pick onto main.
// `fleet merge` rebases before it merges, so a merged branch is often not
// an ancestor. Commits are never matched by subject: reworded but different
// work must not read as merged. Any git error reads as not merged.
func Merged(dir, rev string) bool { return MergedAll(dir, []string{rev})[0] }

// MergedAll is Merged for many revs of one repo at once (`fleet touching`,
// `fleet worktrees` and `fleet branches` ask about every worktree or
// branch): patch ids are computed once per commit, not once per rev, so the
// commits most fleet branches share cost one pass. A rev may be a sha or a
// ref of dir's repo.
func MergedAll(dir string, revs []string) []bool {
	ctx, cancel := context.WithTimeout(context.Background(), mergedTimeout)
	defer cancel()
	return mergedAll(ctx, dir, revs)
}

func mergedAll(ctx context.Context, dir string, revs []string) []bool {
	res := make([]bool, len(revs))
	bases := Bases(dir)
	if len(bases) == 0 {
		return res
	}
	// The commits each rev has beyond main; none means it is an ancestor.
	own := make([][]string, len(revs))
	ok := make([]bool, len(revs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, r := range revs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out, err := gitCtx(ctx, dir, append([]string{"rev-list", "--no-merges", r, "--not"}, bases...)...)
			own[i], ok[i] = strings.Fields(out), err == nil
		}()
	}
	wg.Wait()
	var rest []string
	for i := range revs {
		res[i] = ok[i] && len(own[i]) == 0
		if ok[i] && len(own[i]) > 0 {
			rest = append(rest, revs[i])
		}
	}
	if len(rest) == 0 {
		return res
	}
	// main's side: what `git cherry` compares with for each rev, unioned,
	// i.e. main's commits since the common ancestor of all of them.
	args := append([]string{"rev-list", "--no-merges"}, bases...)
	if mb, err := gitCtx(ctx, dir, append([]string{"merge-base", "--octopus", bases[0]}, rest...)...); err == nil {
		args = append(args, "--not", strings.TrimSpace(mb))
	}
	out, err := gitCtx(ctx, dir, args...)
	if err != nil {
		return res
	}
	theirs := strings.Fields(out)
	ids, err := patchIDs(ctx, dir, append(theirs, flatten(own)...))
	if err != nil {
		return res
	}
	inMain := map[string]bool{}
	for _, c := range theirs {
		if id := ids[c]; id != "" {
			inMain[id] = true
		}
	}
	for i := range revs {
		if res[i] || !ok[i] {
			continue
		}
		res[i] = true
		for _, c := range own[i] {
			// An empty commit has no patch id; like `git cherry`, it counts
			// as not in main.
			if id := ids[c]; id == "" || !inMain[id] {
				res[i] = false
				break
			}
		}
	}
	return res
}

// patchIDs maps each commit to its stable patch id, in one pass for all of
// them; duplicates are read once. An empty commit has none.
func patchIDs(ctx context.Context, dir string, commits []string) (map[string]string, error) {
	seen := map[string]bool{}
	var in strings.Builder
	for _, c := range commits {
		if !seen[c] {
			seen[c] = true
			in.WriteString(c + "\n")
		}
	}
	diff, err := gitIn(ctx, dir, strings.NewReader(in.String()), "diff-tree", "--stdin", "-p", "-r", "--root", "--no-color")
	if err != nil {
		return nil, err
	}
	out, err := gitIn(ctx, dir, strings.NewReader(diff), "patch-id", "--stable")
	if err != nil {
		return nil, err
	}
	ids := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if id, c, ok := strings.Cut(l, " "); ok {
			ids[c] = id
		}
	}
	return ids, nil
}

func flatten(xss [][]string) []string {
	var all []string
	for _, xs := range xss {
		all = append(all, xs...)
	}
	return all
}

// Bases are the refs Merged compares with: the local main (or master),
// then its remote-tracking copy when there is one. nil: neither exists.
func Bases(dir string) []string {
	base := baseBranch(dir)
	if base == "" {
		return nil
	}
	bases := []string{base}
	if _, err := git(dir, "rev-parse", "--verify", "--quiet", "refs/remotes/"+Remote+"/"+base); err == nil {
		bases = append(bases, "refs/remotes/"+Remote+"/"+base)
	}
	return bases
}
