package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestKeepBoth(t *testing.T) {
	in := "head\n<<<<<<< HEAD\n- theirs line\n=======\n- ours line\n>>>>>>> abc (mine)\ntail\n"
	got, ok := KeepBoth(in, true)
	if !ok || got != "head\n- ours line\n- theirs line\ntail\n" {
		t.Fatalf("theirs first: %q ok=%v", got, ok)
	}
	got, _ = KeepBoth(in, false)
	if got != "head\n- theirs line\n- ours line\ntail\n" {
		t.Fatalf("ours first: %q", got)
	}
	// diff3 style: the base section is dropped, both sides kept.
	d3 := "<<<<<<< HEAD\na\n||||||| base\nb\n=======\nc\n>>>>>>> x\n"
	if got, ok := KeepBoth(d3, true); !ok || got != "c\na\n" {
		t.Fatalf("diff3: %q ok=%v", got, ok)
	}
	if _, ok := KeepBoth("no markers\n", true); ok {
		t.Fatal("found a conflict in clean content")
	}
	// An unterminated region is left alone rather than half-resolved.
	if got, ok := KeepBoth("<<<<<<< HEAD\na\n", true); ok || got != "<<<<<<< HEAD\na\n" {
		t.Fatalf("truncated: %q ok=%v", got, ok)
	}
}

// mergeRepo builds a repo whose main checkout has a CHANGELOG and a code file,
// plus one worktree per name, and returns the repo path and the worktrees.
func mergeRepo(t *testing.T, names ...string) (string, map[string]string) {
	t.Helper()
	repo := testRepo(t)
	write(t, repo, "CHANGELOG.md", "# What's new\n\n## 2026-11-05\n- base line\n")
	write(t, repo, "code.go", "package code\n\nconst A = 1\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-q", "-m", "seed")
	wts := map[string]string{}
	for _, n := range names {
		p := filepath.Join(filepath.Dir(repo), "repo.wt", n)
		run(t, repo, "worktree", "add", "-q", "-b", "fleet/"+n, p)
		wts[n] = p
	}
	return repo, wts
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Two agents both add a line to the top of today's CHANGELOG section. The
// first merges cleanly; the second hits the conflict every parallel merge hits
// and must resolve it in place, keeping both lines.
func TestMergeSharedDocConflict(t *testing.T) {
	repo, wts := mergeRepo(t, "a", "b")
	for _, n := range []string{"a", "b"} {
		write(t, wts[n], "CHANGELOG.md", "# What's new\n\n## 2026-11-05\n- from "+n+"\n- base line\n")
		write(t, wts[n], n+".go", "package "+n+"\n")
		run(t, wts[n], "add", ".")
		run(t, wts[n], "commit", "-q", "-m", "feat: "+n)
	}
	checked := map[string]int{}
	check := func(dir string) error { checked[dir]++; return nil }

	if _, err := Merge(Options{Dir: wts["a"], Check: check}); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	p, err := Merge(Options{Dir: wts["b"], Check: check})
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if p.Base != "main" || realPath(p.BaseDir) != realPath(repo) {
		t.Fatalf("plan %+v", p)
	}
	got := read(t, repo, "CHANGELOG.md")
	want := "# What's new\n\n## 2026-11-05\n- from b\n- from a\n- base line\n"
	if got != want {
		t.Fatalf("CHANGELOG after both merges:\n%q\nwant\n%q", got, want)
	}
	for _, n := range []string{"a", "b"} {
		if _, err := os.Stat(filepath.Join(repo, n+".go")); err != nil {
			t.Errorf("%s.go did not reach main: %v", n, err)
		}
		if _, err := os.Stat(wts[n]); err != nil {
			t.Errorf("worktree %s was removed: %v", n, err)
		}
	}
	// Each merge checks the branch and then main again (ADR-48).
	if checked[wts["b"]] != 1 || checked[realPath(repo)]+checked[repo] < 2 {
		t.Fatalf("checks run: %v", checked)
	}
	// Nothing was pushed: the repo has no remote and no stray refs.
	if out, _ := git(repo, "remote"); strings.TrimSpace(out) != "" {
		t.Fatalf("remote %q", out)
	}
}

// A conflict outside the two shared docs is a real overlap: the rebase aborts,
// the branch is untouched, and the error names the file and the other worktree.
func TestMergeNonDocConflictAborts(t *testing.T) {
	repo, wts := mergeRepo(t, "a", "b")
	for _, n := range []string{"a", "b"} {
		write(t, wts[n], "code.go", "package code\n\nconst A = "+n+"\n")
		run(t, wts[n], "commit", "-q", "-am", "feat: "+n)
	}
	if _, err := Merge(Options{Dir: wts["a"]}); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	before, _ := revParse(wts["b"], "HEAD")
	mainBefore, _ := revParse(repo, "main")

	_, err := Merge(Options{Dir: wts["b"]})
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("want a ConflictError, got %v", err)
	}
	if len(ce.Files) != 1 || ce.Files[0] != "code.go" {
		t.Fatalf("files %q", ce.Files)
	}
	if !strings.Contains(ce.Error(), "rebase aborted") {
		t.Fatalf("message %q", ce.Error())
	}
	// Aborted cleanly: no rebase left in progress, branch and main unmoved.
	if after, _ := revParse(wts["b"], "HEAD"); after != before {
		t.Fatalf("branch moved: %s -> %s", before, after)
	}
	if after, _ := revParse(repo, "main"); after != mainBefore {
		t.Fatalf("main moved: %s -> %s", mainBefore, after)
	}
	if u, err := unmergedFiles(wts["b"]); err != nil || len(u) != 0 {
		t.Fatalf("unmerged left behind: %v %v", u, err)
	}
	if _, err := os.Stat(filepath.Join(wts["b"], ".git")); err != nil {
		t.Fatalf("worktree b damaged: %v", err)
	}
}

func TestPlanDryRun(t *testing.T) {
	repo, wts := mergeRepo(t, "a", "b")
	write(t, wts["a"], "code.go", "package code\n\nconst A = 2\n")
	run(t, wts["a"], "commit", "-q", "-am", "feat: a")
	write(t, wts["b"], "code.go", "package code\n\nconst A = 3\n")
	write(t, wts["b"], "b.go", "package b\n")
	run(t, wts["b"], "add", ".")
	run(t, wts["b"], "commit", "-q", "-m", "feat: b")

	p, err := NewPlan(wts["b"])
	if err != nil {
		t.Fatal(err)
	}
	if p.Branch != "fleet/b" || p.Base != "main" || p.Ahead != 1 || p.Behind != 0 {
		t.Fatalf("plan %+v", p)
	}
	if len(p.Neighbours) != 1 || p.Neighbours[0].Branch != "fleet/a" || !p.Neighbours[0].First {
		t.Fatalf("neighbours %+v: a touches fewer files, so it merges first", p.Neighbours)
	}
	steps := strings.Join(p.Steps(), "\n")
	for _, want := range []string{"wait for fleet/a", "rebase fleet/b onto main", "merge fleet/b into main"} {
		if !strings.Contains(steps, want) {
			t.Errorf("steps missing %q:\n%s", want, steps)
		}
	}
	// A dry run changes nothing.
	if out, _ := git(repo, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("dry run dirtied main: %q", out)
	}
}

func TestMergeRefusesDirtyOrMain(t *testing.T) {
	repo, wts := mergeRepo(t, "a")
	if _, err := Merge(Options{Dir: repo}); err == nil || !strings.Contains(err.Error(), "already on main") {
		t.Fatalf("merging from main: %v", err)
	}
	write(t, wts["a"], "code.go", "package code\n\nconst A = 9\n")
	if _, err := Merge(Options{Dir: wts["a"]}); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("dirty worktree: %v", err)
	}
}

// A failing check stops the merge before main is touched.
func TestMergeFailedCheckDoesNotTouchMain(t *testing.T) {
	repo, wts := mergeRepo(t, "a")
	write(t, wts["a"], "a.go", "package a\n")
	run(t, wts["a"], "add", ".")
	run(t, wts["a"], "commit", "-q", "-m", "feat: a")
	before, _ := revParse(repo, "main")
	boom := errors.New("vet failed")
	_, err := Merge(Options{Dir: wts["a"], Check: func(string) error { return boom }})
	if !errors.Is(err, boom) {
		t.Fatalf("want the check error, got %v", err)
	}
	if after, _ := revParse(repo, "main"); after != before {
		t.Fatalf("main moved on a failed check")
	}
}

// With no commits of our own, every other branch trivially "contains" our
// HEAD; the order must come from the file counts instead.
func TestPlanUncommittedOnly(t *testing.T) {
	_, wts := mergeRepo(t, "a", "b")
	write(t, wts["a"], "code.go", "package code\n\nconst A = 2\n")
	write(t, wts["a"], "a.go", "package a\n")
	run(t, wts["a"], "add", ".")
	run(t, wts["a"], "commit", "-q", "-m", "feat: a")
	write(t, wts["b"], "code.go", "package code\n\nconst A = 3\n") // uncommitted

	p, err := NewPlan(wts["b"])
	if err != nil {
		t.Fatal(err)
	}
	if p.Ahead != 0 || len(p.Neighbours) != 1 {
		t.Fatalf("plan %+v", p)
	}
	n := p.Neighbours[0]
	if n.First || !strings.Contains(n.Why, "fewer touched files") {
		t.Fatalf("neighbour %+v: a touches more files, so we go first", n)
	}
}

// DESIGN.md §14 appends ADRs at the bottom in ascending order, so the base's
// entry stays above ours — the opposite of the CHANGELOG (review finding 1).
func TestMergeDesignConflictKeepsAscendingOrder(t *testing.T) {
	repo, wts := mergeRepo(t, "a", "b")
	const base = "## 14. Decisions\n\n**ADR-1: one**\nbody\n"
	write(t, repo, "DESIGN.md", base)
	run(t, repo, "add", "DESIGN.md")
	run(t, repo, "commit", "-q", "-m", "design")
	for _, n := range []string{"a", "b"} {
		run(t, wts[n], "rebase", "-q", "main")
		write(t, wts[n], "DESIGN.md", base+"\n**ADR-"+map[string]string{"a": "2", "b": "3"}[n]+": "+n+"**\nbody\n")
		run(t, wts[n], "commit", "-q", "-am", "adr "+n)
	}
	if _, err := Merge(Options{Dir: wts["a"]}); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	if _, err := Merge(Options{Dir: wts["b"]}); err != nil {
		t.Fatalf("second merge: %v", err)
	}
	got := read(t, repo, "DESIGN.md")
	if i, j := strings.Index(got, "ADR-2"), strings.Index(got, "ADR-3"); i < 0 || j < 0 || i > j {
		t.Fatalf("ADRs out of order:\n%s", got)
	}
}

// main moving while the checks run costs a retry, not a failed merge.
func TestMergeRetriesWhenMainMoves(t *testing.T) {
	repo, wts := mergeRepo(t, "a", "b")
	for _, n := range []string{"a", "b"} {
		write(t, wts[n], n+".go", "package "+n+"\n")
		run(t, wts[n], "add", ".")
		run(t, wts[n], "commit", "-q", "-m", "feat: "+n)
	}
	calls := 0
	check := func(string) error {
		// The first check on b's branch is where a lands on main.
		if calls++; calls == 1 {
			if _, err := Merge(Options{Dir: wts["a"]}); err != nil {
				t.Errorf("a's merge: %v", err)
			}
		}
		return nil
	}
	if _, err := Merge(Options{Dir: wts["b"], Check: check}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if calls < 3 {
		t.Fatalf("checks run %d times: expected a retry (branch, branch again, main)", calls)
	}
	for _, n := range []string{"a", "b"} {
		if _, err := os.Stat(filepath.Join(repo, n+".go")); err != nil {
			t.Errorf("%s.go missing from main: %v", n, err)
		}
	}
}

// A base that keeps moving gives up after Retries rather than spinning.
func TestMergeGivesUpWhenMainNeverSettles(t *testing.T) {
	repo, wts := mergeRepo(t, "a")
	write(t, wts["a"], "a.go", "package a\n")
	run(t, wts["a"], "add", ".")
	run(t, wts["a"], "commit", "-q", "-m", "feat: a")
	n := 0
	check := func(string) error {
		n++
		write(t, repo, "moving.go", "package code\n\nconst N = "+strconv.Itoa(n)+"\n")
		run(t, repo, "add", ".")
		run(t, repo, "commit", "-q", "-m", "main moves")
		return nil
	}
	_, err := Merge(Options{Dir: wts["a"], Check: check, Retries: 2})
	if err == nil || !strings.Contains(err.Error(), "moved during each of 2 attempts") {
		t.Fatalf("want a give-up error, got %v", err)
	}
	if u, e := unmergedFiles(wts["a"]); e != nil || len(u) != 0 {
		t.Fatalf("left mid-rebase: %v %v", u, e)
	}
}

// Result files (task-card output, #11) are not an overlap and don't count
// toward the merge order; a real shared file still does.
func TestPlanIgnoresResultFiles(t *testing.T) {
	_, wts := mergeRepo(t, "a", "b")
	write(t, wts["a"], "result.md", "a's result\n")
	write(t, wts["b"], "result.md", "b's result\n")
	write(t, wts["b"], "b.go", "package b\n")
	ign := []string{filepath.Join(wts["a"], "result.md"), filepath.Join(wts["b"], "result.md")}

	p, err := NewPlan(wts["b"])
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Neighbours) != 1 {
		t.Fatalf("without ignore paths the result files overlap: %+v", p.Neighbours)
	}
	if p, err = NewPlan(wts["b"], ign...); err != nil {
		t.Fatal(err)
	}
	if len(p.Neighbours) != 0 || strings.Join(p.Files, ",") != "b.go" {
		t.Fatalf("plan %+v", p)
	}
	ws, err := Others(wts["b"], ign...)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range ws {
		if w.Path == wts["a"] {
			t.Fatalf("a changes only its result file but is listed: %+v", w)
		}
	}
	write(t, wts["a"], "b.go", "package b // a\n")
	if p, _ = NewPlan(wts["b"], ign...); len(p.Neighbours) != 1 || strings.Join(p.Neighbours[0].Files, ",") != "b.go" {
		t.Fatalf("real overlap: %+v", p.Neighbours)
	}
}
