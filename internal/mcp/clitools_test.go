package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/wall"
)

type cliRun struct {
	dir  string
	argv []string
}

// cliServer is main's admiral server with the fleet binary stubbed: every
// call is recorded and answers out with exit code.
func cliServer(t *testing.T, out string, code int) (*Server, *[]cliRun) {
	t.Helper()
	f := admiralHome(t)
	s := NewAdmiral(f, wall.New(f.T, fleet.Session), "")
	runs := &[]cliRun{}
	s.RunCLI = func(dir string, args ...string) (string, int, error) {
		*runs = append(*runs, cliRun{dir, args})
		return out, code, nil
	}
	return s, runs
}

func TestCLIToolsPruneNeedsConfirmed(t *testing.T) {
	for _, tool := range []string{"fleet_worktrees", "fleet_branches"} {
		s, runs := cliServer(t, "pruned", 0)
		if msg, isErr := callText(t, s, tool, map[string]any{"dir": "/r", "prune": true}); !isErr || !strings.Contains(msg, "confirmed=true") {
			t.Fatalf("%s prune without confirmed: %q %v", tool, msg, isErr)
		}
		if len(*runs) != 0 {
			t.Fatalf("%s ran unconfirmed: %v", tool, *runs)
		}
		if _, isErr := callText(t, s, tool, map[string]any{"dir": "/r", "prune": true, "confirmed": true}); isErr {
			t.Fatalf("%s confirmed prune refused", tool)
		}
		if _, isErr := callText(t, s, tool, map[string]any{"dir": "/r"}); isErr {
			t.Fatalf("%s list refused", tool)
		}
		cmd := strings.TrimPrefix(tool, "fleet_")
		want := []cliRun{{"/r", []string{cmd, "--repo", "/r", "--prune"}}, {"/r", []string{cmd, "--repo", "/r"}}}
		if !reflect.DeepEqual(*runs, want) {
			t.Fatalf("%s argv %v, want %v", tool, *runs, want)
		}
		if msg, isErr := callText(t, s, tool, map[string]any{"dir": "rel"}); !isErr || !strings.Contains(msg, "absolute") {
			t.Fatalf("%s relative dir: %q", tool, msg)
		}
	}
}

// fleet_touching runs in process: an overlap is an answer, not a failure,
// and a dir outside git is an error, never "no overlap".
func TestCLIToolsTouchingOverlap(t *testing.T) {
	s, runs := cliServer(t, "", 0)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	git := func(dir string, args ...string) {
		t.Helper()
		args = append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid"}, args...)
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.MkdirAll(repo, 0o755)
	git(repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "a.go"), []byte("a\n"), 0o644)
	git(repo, "add", ".")
	git(repo, "commit", "-q", "-m", "init")
	wt := filepath.Join(root, "repo.wt", "x")
	git(repo, "worktree", "add", "-q", "-b", "fleet/x", wt)
	os.WriteFile(filepath.Join(wt, "a.go"), []byte("a2\n"), 0o644)

	msg, isErr := callText(t, s, "fleet_touching", map[string]any{"dir": repo, "files": []string{"a.go"}})
	if isErr || !strings.HasPrefix(msg, "overlap:\n") || !strings.Contains(msg, "fleet/x  1 files\n    a.go") {
		t.Fatalf("%q %v", msg, isErr)
	}
	if msg, isErr := callText(t, s, "fleet_touching", map[string]any{"dir": repo, "files": []string{"b.go"}}); isErr || msg != "no other worktree touches these files" {
		t.Fatalf("no overlap: %q %v", msg, isErr)
	}
	if msg, isErr := callText(t, s, "fleet_touching", map[string]any{"dir": root}); !isErr || !strings.Contains(msg, "git") {
		t.Fatalf("outside git: %q %v", msg, isErr)
	}
	if len(*runs) != 0 {
		t.Fatalf("forked: %v", *runs)
	}
}

// Takeover is a write: main can't take print's agent.
func TestCLIToolsTakeoverScoped(t *testing.T) {
	s, runs := cliServer(t, "", 0)
	if msg, isErr := callText(t, s, "fleet_takeover", map[string]any{"name": "p1"}); !isErr || !strings.Contains(msg, "belongs to admiral print") {
		t.Fatalf("%q %v", msg, isErr)
	}
	if len(*runs) != 0 {
		t.Fatalf("ran: %v", *runs)
	}
	f := s.F
	print := NewAdmiral(f, wall.New(f.T, fleet.Session), "print")
	print.RunCLI = s.RunCLI
	if _, isErr := callText(t, print, "fleet_takeover", map[string]any{"name": "p1", "acp": true}); isErr {
		t.Fatal("print refused its own agent")
	}
	if want := []string{"takeover", "p1", "--acp"}; !reflect.DeepEqual((*runs)[0].argv, want) {
		t.Fatalf("argv %v", (*runs)[0].argv)
	}
}

func TestCLIToolsAdmiralAddAndChanges(t *testing.T) {
	s, runs := cliServer(t, "1\n2\n3\n4", 0)
	if _, isErr := callText(t, s, "fleet_admiral_add", map[string]any{"name": "ops", "profile": "fake"}); isErr {
		t.Fatal("add refused")
	}
	if want := []string{"admiral", "add", "ops", "--profile", "fake"}; !reflect.DeepEqual((*runs)[0].argv, want) {
		t.Fatalf("argv %v", (*runs)[0].argv)
	}
	if len(*runs) != 1 {
		t.Fatalf("runs %v", *runs)
	}
	// fleet_changes reads the changelog built into the binary, in process.
	if msg, _ := callText(t, s, "fleet_changes", map[string]any{"limit": 2}); len(strings.Split(msg, "\n")) != 3 || !strings.Contains(msg, "more lines: limit=N for more)") {
		t.Fatalf("changes: %q", msg)
	}
	if len(*runs) != 1 {
		t.Fatalf("changes forked: %v", *runs)
	}
}

// #18: only main closes an admiral, through `fleet admiral close`.
func TestCLIToolsAdmiralClose(t *testing.T) {
	s, runs := cliServer(t, "admiral print closed", 0)
	if msg, isErr := callText(t, s, "fleet_admiral_close", map[string]any{"name": "print"}); isErr {
		t.Fatalf("close refused: %q", msg)
	}
	if want := []string{"admiral", "close", "print"}; len(*runs) != 1 || !reflect.DeepEqual((*runs)[0].argv, want) {
		t.Fatalf("runs %v", *runs)
	}
	print := NewAdmiral(s.F, wall.New(s.F.T, fleet.Session), "print")
	print.RunCLI = s.RunCLI
	if msg, isErr := callText(t, print, "fleet_admiral_close", map[string]any{"name": "print"}); !isErr || !strings.Contains(msg, "only main") {
		t.Fatalf("print closed an admiral: %q", msg)
	}
	if len(*runs) != 1 {
		t.Fatal("refused close still ran the CLI")
	}
}

// fleet_recall runs in process: a query searches the notes, no query is
// the day digest; without cass both say how to get session search.
func TestRecallTool(t *testing.T) {
	t.Setenv("FLEET_CASS", "off")
	s, runs := cliServer(t, "", 0)
	if _, isErr := callText(t, s, "fleet_note", map[string]any{"text": "decision: integrate cass"}); isErr {
		t.Fatal("note refused")
	}
	msg, isErr := callText(t, s, "fleet_recall", map[string]any{"query": "Cass"})
	if isErr || !strings.Contains(msg, "decision: integrate cass") || !strings.Contains(msg, "cass is not installed") {
		t.Fatalf("recall: %q", msg)
	}
	msg, isErr = callText(t, s, "fleet_recall", nil)
	if isErr || !strings.Contains(msg, "## Waiting on you") || !strings.Contains(msg, "p1 ") {
		t.Fatalf("day: %q", msg)
	}
	if len(*runs) != 0 {
		t.Fatalf("recall forked: %v", *runs)
	}
}
