package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fleet run starts a plan's first task on a fleet-test socket, the second
// once the first reports done, and --status shows where each stands.
func TestCLIRunPlan(t *testing.T) {
	f := spawnFleet(t)
	runWith(t, f)
	dir := t.TempDir()
	path := filepath.Join(t.TempDir(), "ship.toml")
	os.WriteFile(path, fmt.Appendf(nil, "[task.a]\nprofile = \"cat\"\ndir = %q\nprompt = \"do a\"\n\n[task.b]\nprofile = \"cat\"\ndir = %q\nprompt = \"do b\"\nafter = [\"a\"]\n", dir, dir), 0o644)
	out := captureOut(t, func() error { return run([]string{"run", path}) })
	if !strings.Contains(out, "plan ship:") || !strings.Contains(out, "a                    write") || !strings.Contains(out, "b                    pending") {
		t.Fatalf("run printed:\n%s", out)
	}
	if _, err := f.Report("a", "done", "did a"); err != nil {
		t.Fatal(err)
	}
	out = captureOut(t, func() error { return run([]string{"run", "--status"}) })
	if !strings.Contains(out, "a                    done") || !strings.Contains(out, "b                    write") {
		t.Fatalf("status printed:\n%s", out)
	}
	if f.Store.Load().Agents["b"] == nil {
		t.Fatal("b not spawned")
	}
	if err := run([]string{"run"}); err == nil || !strings.Contains(err.Error(), "usage: fleet run") {
		t.Fatalf("no args: %v", err)
	}
}
