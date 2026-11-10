package mcp

import (
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

// Exit 1 from fleet touching is the overlap answer, not a failure.
func TestCLIToolsTouchingOverlap(t *testing.T) {
	s, runs := cliServer(t, "a.go  fleet/x\n", 1)
	msg, isErr := callText(t, s, "fleet_touching", map[string]any{"dir": "/r", "files": []string{"a.go"}})
	if isErr || !strings.HasPrefix(msg, "overlap:\na.go") {
		t.Fatalf("%q %v", msg, isErr)
	}
	if want := []string{"touching", "-C", "/r", "a.go"}; !reflect.DeepEqual((*runs)[0].argv, want) {
		t.Fatalf("argv %v", (*runs)[0].argv)
	}
	s, _ = cliServer(t, "boom", 2)
	if msg, isErr := callText(t, s, "fleet_touching", map[string]any{"dir": "/r"}); !isErr || msg != "boom" {
		t.Fatalf("exit 2: %q %v", msg, isErr)
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
	if msg, _ := callText(t, s, "fleet_changes", map[string]any{"limit": 2}); msg != "1\n2\n(2 more lines: limit=N for more)" {
		t.Fatalf("changes: %q", msg)
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
