package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A repo with one ADR on main, so "next free" has something to read.
func adrRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "DESIGN.md"), []byte("**ADR-42: fixtures**\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "DESIGN.md")
	git("commit", "-qm", "design")
	return dir
}

func TestADRClaimPrintsTheNumberAndListsIt(t *testing.T) {
	t.Setenv("FLEET_HOME", t.TempDir())
	t.Setenv("TMUX_PANE", "") // not an agent pane
	repo := adrRepo(t)

	out := captureOut(t, func() error { return cmdADR([]string{"claim", "--for", "a1", "-C", repo}) })
	n := strings.TrimSpace(out)
	if n != "51" { // the seeded hand-outs top out at ADR-50
		t.Fatalf("claim printed %q, want 51", n)
	}
	out = captureOut(t, func() error { return cmdADR([]string{"ls", "-C", repo}) })
	if !strings.Contains(out, "51") || !strings.Contains(out, "a1") {
		t.Fatalf("ls output %q, want the claim for a1", out)
	}
	if err := cmdADR([]string{"release", "51", "-C", repo}); err != nil {
		t.Fatal(err)
	}
	out = captureOut(t, func() error { return cmdADR([]string{"ls", "--json", "-C", repo}) })
	if strings.Contains(out, "\"n\": 51") {
		t.Fatalf("ADR-51 still listed after release: %s", out)
	}
}

func TestADRRejectsBadInput(t *testing.T) {
	t.Setenv("FLEET_HOME", t.TempDir())
	repo := adrRepo(t)
	for name, args := range map[string][]string{
		"no subcommand":  {},
		"unknown":        {"nope"},
		"release no arg": {"release", "-C", repo},
		"release NaN":    {"release", "x", "-C", repo},
		"not a repo":     {"claim", "-C", t.TempDir()},
		"claim extra":    {"claim", "someone", "-C", repo},
		"ls extra":       {"ls", "51", "-C", repo},
		"release --for":  {"release", "51", "--for", "x", "-C", repo},
		"claim --json":   {"claim", "--json", "-C", repo},
	} {
		if err := cmdADR(args); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// captureOut runs fn with stdout redirected and returns what it printed.
func captureOut(t *testing.T, fn func() error) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	runErr := fn()
	os.Stdout = old
	w.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	r.Close()
	if runErr != nil {
		t.Fatal(runErr)
	}
	return b.String()
}
