package rexec

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMakeRunsInTheSameSubdirOfACopyPerWorktree(t *testing.T) {
	p := Make("box", "", "/w/fleet.wt/a", "/w/fleet.wt/a/internal/x", []string{"go", "test", "./..."})
	if !strings.HasPrefix(p.Dir, "fleet-exec/a-") || !strings.HasSuffix(p.Dir, "/internal/x") {
		t.Fatalf("dir %q", p.Dir)
	}
	if RemoteDir("", "/w/fleet.wt/a") == RemoteDir("", "/w/other/a") {
		t.Error("two worktrees with one base name share a copy")
	}
	if got := strings.Join(p.Sync, " "); !strings.Contains(got, "/w/fleet.wt/a/ box:fleet-exec/a-") || !strings.Contains(got, "--exclude=/.git") {
		t.Errorf("sync %q", got)
	}
	if last := p.Run[len(p.Run)-1]; last != "cd "+p.Dir+" && go test ./..." {
		t.Errorf("run %q", last)
	}
}

// The remote line must survive a real shell: quoted words stay words, and
// a single argument keeps its pipes.
func TestLineThroughAShell(t *testing.T) {
	for _, c := range []struct {
		cmd  []string
		want string
	}{
		{[]string{"printf", "%s|", "a b", "it's", ""}, "a b|it's||"},
		{[]string{"echo hi | tr a-z A-Z"}, "HI\n"},
	} {
		out, err := exec.Command("sh", "-c", Line(c.cmd)).Output()
		if err != nil || string(out) != c.want {
			t.Errorf("%q: %q %v, want %q", c.cmd, out, err, c.want)
		}
	}
}

func TestResolve(t *testing.T) {
	dir := t.TempDir()
	for _, b := range []string{"rsync", "ssh"} {
		if err := os.WriteFile(filepath.Join(dir, b), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	p, err := Make("h", "", "/w", "/w", []string{"true"}).Resolve()
	if err != nil || p.Sync[0] != filepath.Join(dir, "rsync") || p.Run[0] != filepath.Join(dir, "ssh") || p.Run[1] != "-T" {
		t.Fatalf("got %v %v", p, err)
	}
	os.Remove(filepath.Join(dir, "ssh"))
	if _, err := Make("h", "", "/w", "/w", []string{"true"}).Resolve(); err == nil || !strings.Contains(err.Error(), "ssh is not installed") {
		t.Errorf("missing ssh: %v", err)
	}
}
