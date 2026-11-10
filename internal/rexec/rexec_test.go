package rexec

import (
	"os/exec"
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
