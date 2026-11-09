package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sturlabragason/fleet/internal/board"
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
