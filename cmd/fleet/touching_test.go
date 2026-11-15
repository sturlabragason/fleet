package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/graph"
	"github.com/sturlabragason/fleet/internal/logx"
)

// Errors exit 2, never 0 or 1, so a failed check can't read as all-clear or
// as an overlap.
func TestTouchingErrorsExit2(t *testing.T) {
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	notRepo := t.TempDir()
	for name, args := range map[string][]string{
		"not a repo": {"-C", notRepo, "a.go"},
		"bad flag":   {"--nope"},
	} {
		var code exitCode
		if err := cmdTouching(args); !errors.As(err, &code) || code != 2 {
			t.Errorf("%s: err %v, want exit 2", name, err)
		}
	}
}

// Every board agent's result file is left out of the overlap (#11).
func TestTouchingIgnoresResultFiles(t *testing.T) {
	t.Setenv("FLEET_HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	repo := filepath.Join(t.TempDir(), "repo")
	other := filepath.Join(filepath.Dir(repo), "repo.wt", "a")
	gitT := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.MkdirAll(repo, 0o755)
	gitT(repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "x.go"), []byte("package x\n"), 0o644)
	gitT(repo, "add", ".")
	gitT(repo, "commit", "-q", "-m", "seed")
	gitT(repo, "worktree", "add", "-q", "-b", "fleet/a", other)
	os.WriteFile(filepath.Join(other, "a.go"), []byte("package x\n"), 0o644) // unmerged work, so a is listed
	gitT(other, "add", ".")
	gitT(other, "commit", "-q", "-m", "a")
	os.WriteFile(filepath.Join(other, "result.md"), []byte("done\n"), 0o644)

	var code exitCode
	if err := cmdTouching([]string{"-C", repo, "result.md"}); !errors.As(err, &code) || code != 1 {
		t.Fatalf("no agent on the board: err %v, want the overlap (exit 1)", err)
	}
	board.NewStore(logx.Home()).Update(func(b *board.Board) error {
		b.Agents["a"] = &board.Agent{Name: "a", Dir: other, Card: &board.Card{Result: "result.md"}}
		return nil
	})
	if err := cmdTouching([]string{"-C", repo, "result.md"}); err != nil {
		t.Fatalf("a's result file counted as an overlap: %v", err)
	}
}

// --graph lists a worktree whose changes are one call away from the files
// but exits 1 only on a shared file; without a graph it is an error, not
// all-clear.
func TestTouchingGraph(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	repo := filepath.Join(t.TempDir(), "repo")
	other := filepath.Join(filepath.Dir(repo), "repo.wt", "b")
	gitT := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.MkdirAll(repo, 0o755)
	gitT(repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "a.go"), []byte("package x\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "b.go"), []byte("package x\n"), 0o644)
	gitT(repo, "add", ".")
	gitT(repo, "commit", "-q", "-m", "seed")
	gitT(repo, "worktree", "add", "-q", "-b", "fleet/b", other)
	os.WriteFile(filepath.Join(other, "b.go"), []byte("package x\n\nfunc B() {}\n"), 0o644)
	gitT(other, "commit", "-qam", "b")

	var code exitCode
	if err := cmdTouching([]string{"-C", repo, "--graph", "a.go"}); !errors.As(err, &code) || code != 2 {
		t.Fatalf("no graph: err %v, want exit 2", err)
	}
	root, err := graph.Root(repo)
	if err != nil {
		t.Fatal(err)
	}
	out := graph.Dir(home, root)
	os.MkdirAll(out, 0o755)
	os.WriteFile(filepath.Join(out, "fleet-graph.json"), []byte(`{"schema_version":1}`), 0o644)
	os.WriteFile(filepath.Join(out, "graph.json"), []byte(`{"nodes":[{"id":"a","source_file":"a.go"},{"id":"b","source_file":"b.go"}],`+
		`"links":[{"source":"a","target":"b","relation":"calls"}]}`), 0o644)
	cases := []struct {
		name string
		args []string
		want int
		out  string
	}{
		{"without --graph", []string{"-C", repo, "a.go"}, 0, "no other worktree touches"},
		{"a.go calls b.go", []string{"-C", repo, "--graph", "a.go"}, 0, "your a.go uses its b.go"},
		{"both change b.go", []string{"-C", repo, "--graph", "b.go"}, 1, "both change b.go"},
		{"c.go is unrelated", []string{"-C", repo, "--graph", "c.go"}, 0, "no other worktree changes these files"},
	}
	for _, c := range cases {
		var err error
		out := captureOut(t, func() error { err = cmdTouching(c.args); return nil })
		got := 0
		if errors.As(err, &code) {
			got = int(code)
		}
		if got != c.want || !strings.Contains(out, c.out) {
			t.Errorf("%s: err %v, want exit %d; output %q, want %q", c.name, err, c.want, out, c.out)
		}
	}
}
