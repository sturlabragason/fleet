package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

const porcelain = `worktree /src/repo
HEAD 1111111111111111111111111111111111111111
branch refs/heads/main

worktree /src/repo.wt/a
HEAD 2222222222222222222222222222222222222222
branch refs/heads/feat/a

worktree /src/repo.wt/b
HEAD 3333333333333333333333333333333333333333
detached

worktree /src/bare.git
bare

worktree /src/repo.wt/gone
HEAD 4444444444444444444444444444444444444444
branch refs/heads/old
prunable gitdir file points to non-existent location
`

func TestParse(t *testing.T) {
	ws := Parse(porcelain)
	if len(ws) != 5 {
		t.Fatalf("got %d worktrees: %+v", len(ws), ws)
	}
	want := []struct {
		path, branch   string
		bare, prunable bool
	}{
		{"/src/repo", "main", false, false},
		{"/src/repo.wt/a", "feat/a", false, false},
		{"/src/repo.wt/b", "(detached)", false, false},
		{"/src/bare.git", "(bare)", true, false},
		{"/src/repo.wt/gone", "old", false, true},
	}
	for i, w := range want {
		g := ws[i]
		if g.Path != w.path || g.Branch != w.branch || g.Bare != w.bare || g.Prunable != w.prunable {
			t.Errorf("%d: %+v want %+v", i, g, w)
		}
	}
	if ws[1].Head != "2222222222222222222222222222222222222222" {
		t.Errorf("head %q", ws[1].Head)
	}
}

func TestParseStatus(t *testing.T) {
	out := " M a.go\x00?? new/file.txt\x00R  b2.go\x00b.go\x00A  c.go\x00"
	got := ParseStatus(out)
	want := []string{"a.go", "new/file.txt", "b2.go", "b.go", "c.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q want %q", got, want)
	}
}

func TestOverlap(t *testing.T) {
	ws := []Worktree{
		{Path: "/a", Files: []string{"x.go", "y.go"}},
		{Path: "/b", Files: []string{"z.go"}},
	}
	got := Overlap(ws, []string{"./y.go", "q.go"})
	if len(got) != 1 || got[0].Path != "/a" || !reflect.DeepEqual(got[0].Files, []string{"y.go"}) {
		t.Fatalf("%+v", got)
	}
	if len(ws[0].Files) != 2 {
		t.Error("Overlap mutated its input")
	}
	if got := Overlap(ws, []string{"none.go"}); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestRepoRel(t *testing.T) {
	top := t.TempDir()
	os.MkdirAll(filepath.Join(top, "internal", "x"), 0o755)
	os.WriteFile(filepath.Join(top, "internal", "x", "f.go"), nil, 0o644)
	sub := filepath.Join(top, "internal")
	cases := map[string]string{
		"x/f.go":                     "internal/x/f.go", // relative to cwd
		"internal/new.go":            "internal/new.go", // not here: taken as repo-relative
		filepath.Join(sub, "x/f.go"): "internal/x/f.go", // absolute
	}
	for arg, want := range cases {
		if got := RepoRel(top, sub, arg); got != want {
			t.Errorf("RepoRel(%q) = %q want %q", arg, got, want)
		}
	}
}

func TestOthersIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// Keep the user's git config out; give commits an identity.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.invalid")
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	run := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.MkdirAll(repo, 0o755)
	run(repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "a.go"), []byte("a\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "b.go"), []byte("b\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "c.go"), []byte("package c\n\nfunc C() {}\n"), 0o644)
	run(repo, "add", ".")
	run(repo, "commit", "-q", "-m", "init")
	wt := filepath.Join(root, "repo.wt", "x")
	run(repo, "worktree", "add", "-q", "-b", "feat/x", wt)
	os.WriteFile(filepath.Join(wt, "a.go"), []byte("a2\n"), 0o644)
	run(wt, "commit", "-q", "-am", "change a")
	run(wt, "mv", "c.go", "moved.go")
	run(wt, "commit", "-q", "-m", "rename c")
	os.WriteFile(filepath.Join(wt, "b.go"), []byte("b2\n"), 0o644) // uncommitted
	os.MkdirAll(filepath.Join(wt, "pkg"), 0o755)
	os.WriteFile(filepath.Join(wt, "pkg", "new.go"), nil, 0o644) // untracked

	ws, err := Others(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 || ws[0].Branch != "feat/x" {
		t.Fatalf("others from main: %+v", ws)
	}
	if want := []string{"a.go", "b.go", "c.go", "moved.go", "pkg/new.go"}; !reflect.DeepEqual(ws[0].Files, want) {
		t.Fatalf("files %q want %q", ws[0].Files, want)
	}
	// From inside the worktree: itself is excluded, the clean main checkout
	// is skipped.
	if ws, err := Others(filepath.Join(wt, "pkg")); err != nil || len(ws) != 0 {
		t.Fatalf("others from worktree: %+v %v", ws, err)
	}
	// Worktrees of one repo share a common dir; another repo doesn't, even
	// with GIT_DIR inherited from a hook.
	t.Setenv("GIT_DIR", filepath.Join(repo, ".git"))
	c1, err1 := CommonDir(filepath.Join(wt, "pkg"))
	c2, err2 := CommonDir(repo)
	if err1 != nil || err2 != nil || c1 != c2 {
		t.Fatalf("common dir: %q %v / %q %v", c1, err1, c2, err2)
	}
	other := filepath.Join(root, "other")
	os.MkdirAll(other, 0o755)
	run(other, "init", "-q")
	if c3, _ := CommonDir(other); c3 == c1 {
		t.Fatalf("other repo shares common dir %q", c3)
	}
	if _, err := CommonDir(root); err == nil {
		t.Fatal("common dir outside a repo")
	}
	os.Unsetenv("GIT_DIR")
	// Fail closed: a worktree whose git state can't be read is an error, not
	// "touches nothing".
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+filepath.Join(root, "missing")+"\n"), 0o644)
	if ws, err := Others(repo); err == nil {
		t.Fatalf("broken worktree gave no error: %+v", ws)
	}
}

func TestChangesOutsideRepoFails(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	if files, err := Changes(t.TempDir()); err == nil {
		t.Fatalf("no error outside a repo: %q", files)
	}
}

func TestTouchingText(t *testing.T) {
	ws := []Worktree{{Path: "/r.wt/x", Branch: "fleet/x", Files: []string{"a.go", "b.go"}}}
	if got := TouchingText(ws, true); got != "/r.wt/x  fleet/x  2 files\n    a.go\n    b.go\n" {
		t.Errorf("%q", got)
	}
	if got := TouchingText(nil, true); got != "no other worktree touches these files\n" {
		t.Errorf("%q", got)
	}
	if got := TouchingText(nil, false); got != "" {
		t.Errorf("%q", got)
	}
}
