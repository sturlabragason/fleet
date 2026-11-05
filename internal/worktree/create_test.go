package worktree

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// testRepo makes a temp repo with one commit on main and returns its path.
func testRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.invalid")
	repo := filepath.Join(t.TempDir(), "repo")
	os.MkdirAll(repo, 0o755)
	run(t, repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "a.go"), []byte("a\n"), 0o644)
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-q", "-m", "init")
	return repo
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestCreate(t *testing.T) {
	repo := testRepo(t)
	w, err := Create(repo, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if want := Path(realPath(repo), "a1"); realPath(want) != w.Path {
		t.Fatalf("path %q want %q", w.Path, want)
	}
	if w.Branch != "fleet/a1" || w.Repo != realPath(repo) {
		t.Fatalf("branch %q repo %q", w.Branch, w.Repo)
	}
	if _, err := os.Stat(filepath.Join(w.Path, "a.go")); err != nil {
		t.Fatalf("worktree has no checkout: %v", err)
	}
	// It is a real worktree of the repo, on its own branch.
	ws, err := Others(repo)
	if err != nil || len(ws) != 0 {
		t.Fatalf("a fresh worktree changes nothing: %+v %v", ws, err)
	}
	// Spawning the same name again reuses the worktree, keeping its work.
	os.WriteFile(filepath.Join(w.Path, "b.go"), []byte("b\n"), 0o644)
	again, err := Create(repo, "a1")
	if err != nil || again.Path != w.Path || again.Branch != w.Branch {
		t.Fatalf("reuse: %+v %v", again, err)
	}
	if _, err := os.Stat(filepath.Join(w.Path, "b.go")); err != nil {
		t.Fatalf("reuse lost the work: %v", err)
	}
	// A second agent in the same repo gets its own worktree and branch.
	w2, err := Create(repo, "a2")
	if err != nil || w2.Path == w.Path || w2.Branch != "fleet/a2" {
		t.Fatalf("second agent: %+v %v", w2, err)
	}
	// Created from inside a linked worktree, the next one still hangs off
	// the main checkout rather than nesting.
	w3, err := Create(w.Path, "a3")
	if err != nil {
		t.Fatal(err)
	}
	if want := realPath(Path(realPath(repo), "a3")); w3.Path != want {
		t.Fatalf("from a worktree: %q want %q", w3.Path, want)
	}
}

func TestCreateOutsideGitAndWhenGitRefuses(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	if _, err := Create(t.TempDir(), "a1"); !errors.Is(err, ErrNoRepo) {
		t.Fatalf("outside a repo: %v", err)
	}
	if _, err := Create("", "a1"); !errors.Is(err, ErrNoRepo) {
		t.Fatalf("empty dir: %v", err)
	}
	// A repo with no commit yet still works: git infers an orphan branch.
	empty := t.TempDir()
	run(t, empty, "init", "-q", "-b", "main")
	if w, err := Create(empty, "a1"); err != nil || w.Branch != "fleet/a1" {
		t.Fatalf("unborn HEAD: %+v %v", w, err)
	}
	// Something else already sitting at the path is an error, and a plain
	// one, so spawn warns and falls back instead of staying silent.
	repo := testRepo(t)
	taken := Path(realPath(repo), "a1")
	os.MkdirAll(taken, 0o755)
	if _, err := Create(repo, "a1"); err == nil || errors.Is(err, ErrNoRepo) {
		t.Fatalf("path taken by a plain dir: %v", err)
	}
}

func TestOf(t *testing.T) {
	repo := testRepo(t)
	if w := Of(repo); w != nil {
		t.Fatalf("main worktree: %+v", w)
	}
	if w := Of(t.TempDir()); w != nil {
		t.Fatalf("outside a repo: %+v", w)
	}
	c, err := Create(repo, "a1")
	if err != nil {
		t.Fatal(err)
	}
	w := Of(c.Path)
	if w == nil || w.Path != c.Path || w.Branch != "fleet/a1" || w.Repo != realPath(repo) {
		t.Fatalf("linked worktree: %+v", w)
	}
}

// A worktree dir someone deleted by hand leaves a stale registration behind;
// the next spawn of that name must get a real directory, not the ghost.
func TestCreateAfterTheWorktreeDirIsDeleted(t *testing.T) {
	repo := testRepo(t)
	w, err := Create(repo, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(w.Path); err != nil {
		t.Fatal(err)
	}
	again, err := Create(repo, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if again.Path != w.Path || again.Branch != "fleet/a1" {
		t.Fatalf("got %+v want path %q on fleet/a1", again, w.Path)
	}
	if _, err := os.Stat(filepath.Join(again.Path, "a.go")); err != nil {
		t.Fatalf("worktree was not checked out again: %v", err)
	}
}
