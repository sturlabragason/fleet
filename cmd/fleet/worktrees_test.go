package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	for _, n := range []string{"free", "held", "shut", "old"} {
		gitT(repo, "worktree", "add", "-q", "-b", "fleet/"+n, worktree.Path(repo, n))
	}
	b := &board.Board{Agents: map[string]*board.Agent{
		"held": {Name: "held", Worktree: &board.Worktree{Path: worktree.Path(repo, "held")}},
	}}
	// A recently closed agent shows; its merged worktree can still go, since
	// the work is in main (ADR-132). One closed past ClosedKeep doesn't show.
	b.AddClosed(&board.Agent{Name: "shut", Worktree: &board.Worktree{Path: worktree.Path(repo, "shut")}}, "", time.Now())
	b.LegacyClosed = map[string]*board.Closed{"old": {Agent: board.Agent{Name: "old", Worktree: &board.Worktree{Path: worktree.Path(repo, "old")}}, At: time.Now().Add(-board.ClosedKeep - time.Hour)}}

	var out bytes.Buffer
	if err := runWorktrees(b, nil, []string{"--repo", repo, "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var es []worktree.Entry
	if err := json.Unmarshal(out.Bytes(), &es); err != nil || len(es) != 4 {
		t.Fatalf("json: %v %s", err, out.String())
	}
	want := map[string]string{"free": "", "held": "live", "shut": "closed", "old": ""}
	for _, e := range es {
		if e.Agent != want[e.Name] || e.Prunable != (want[e.Name] != "live") {
			t.Errorf("%+v", e)
		}
	}
	out.Reset()
	runWorktrees(b, nil, []string{"--repo", repo}, &out)
	if !strings.Contains(out.String(), "4 worktrees") || !strings.Contains(out.String(), "3 can be pruned") {
		t.Errorf("table:\n%s", out.String())
	}
	out.Reset()
	if err := runWorktrees(b, nil, []string{"--repo", repo, "--prune"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "branch fleet/free deleted") {
		t.Errorf("prune output: %s", out.String())
	}
	if _, err := os.Stat(worktree.Path(repo, "held")); err != nil {
		t.Error("held worktree removed")
	}
	if _, err := os.Stat(worktree.Path(repo, "shut")); err == nil {
		t.Error("shut's merged worktree kept")
	}

	// A board.json or closed.json that cannot be read refuses a prune: it
	// reads as no agents. Listing still works.
	for _, file := range []string{"board.json", "closed.json"} {
		home := t.TempDir()
		os.WriteFile(filepath.Join(home, file), []byte("{nope"), 0o644)
		nb, bad := loadBoard(board.NewStore(home))
		if err := runWorktrees(nb, bad, []string{"--repo", repo, "--prune"}, &out); err == nil || !strings.Contains(err.Error(), "not pruning") {
			t.Errorf("prune with broken %s: %v", file, err)
		}
		if err := runWorktrees(nb, bad, []string{"--repo", repo}, &out); err != nil {
			t.Errorf("list with broken %s: %v", file, err)
		}
	}
	if _, err := os.Stat(worktree.Path(repo, "held")); err != nil {
		t.Error("held worktree removed with the board unreadable")
	}
	if err := runWorktrees(b, nil, []string{"--repo", t.TempDir()}, &out); err == nil || !strings.Contains(err.Error(), "not in a git repo") {
		t.Errorf("non-repo: %v", err)
	}
}
