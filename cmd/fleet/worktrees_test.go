package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/worktree"
)

func TestWorktreesCommand(t *testing.T) {
	t.Setenv("FLEET_CONFIG", filepath.Join(t.TempDir(), "none.toml"))
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	repo := filepath.Join(t.TempDir(), "repo")
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
	for _, n := range []string{"free", "held"} {
		gitT(repo, "worktree", "add", "-q", "-b", "fleet/"+n, worktree.Path(repo, n))
	}
	b := &board.Board{Agents: map[string]*board.Agent{
		"held": {Name: "held", Worktree: &board.Worktree{Path: worktree.Path(repo, "held")}},
	}}

	var out bytes.Buffer
	if err := runWorktrees(b, []string{"--repo", repo, "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var es []worktree.Entry
	if err := json.Unmarshal(out.Bytes(), &es); err != nil || len(es) != 2 {
		t.Fatalf("json: %v %s", err, out.String())
	}
	for _, e := range es {
		if (e.Name == "free") != e.Prunable || (e.Name == "held") != (e.Agent == "live") {
			t.Errorf("%+v", e)
		}
	}
	out.Reset()
	runWorktrees(b, []string{"--repo", repo}, &out)
	if !strings.Contains(out.String(), "2 worktrees") || !strings.Contains(out.String(), "1 can be pruned") {
		t.Errorf("table:\n%s", out.String())
	}
	out.Reset()
	if err := runWorktrees(b, []string{"--repo", repo, "--prune"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "branch fleet/free deleted") {
		t.Errorf("prune output: %s", out.String())
	}
	if _, err := os.Stat(worktree.Path(repo, "held")); err != nil {
		t.Error("held worktree removed")
	}
	if err := runWorktrees(b, []string{"--repo", t.TempDir()}, &out); err == nil || !strings.Contains(err.Error(), "not in a git repo") {
		t.Errorf("non-repo: %v", err)
	}
}
