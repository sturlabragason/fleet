package main

import (
	"bytes"
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// reportsHome is a fleet with two board agents and no tmux of its own, so
// reports land in the mailbox without waking anything.
func reportsHome(t *testing.T) *fleet.Fleet {
	t.Helper()
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := fleet.New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	f.SupPane = func() fleet.AdmiralView { return fleet.AdmiralView{} }
	if err := f.Store.Update(func(b *board.Board) error {
		for _, n := range []string{"a", "b"} {
			b.Agents[n] = &board.Agent{Name: n, Profile: "fake", Dir: home}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func reportsOut(t *testing.T, f *fleet.Fleet, as string, all bool, limit int) string {
	t.Helper()
	var b bytes.Buffer
	if err := writeReports(&b, f, as, all, limit); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestCmdReports(t *testing.T) {
	f := reportsHome(t)
	if got := reportsOut(t, f, "", false, 20); !strings.Contains(got, "no new reports") {
		t.Fatalf("empty box: %q", got)
	}
	if _, err := f.Report("a", "done", "nav fixed on feat/nav"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Report("b", "blocked", "need the schema"); err != nil {
		t.Fatal(err)
	}

	// Unread, oldest first, and reading marks them read.
	got := reportsOut(t, f, "", false, 20)
	if !strings.Contains(got, " a: done: nav fixed on feat/nav\n") || !strings.Contains(got, " b: blocked: need the schema\n") {
		t.Fatalf("unread reports: %q", got)
	}
	if i, j := strings.Index(got, "a: done"), strings.Index(got, "b: blocked"); i > j {
		t.Fatalf("not oldest first: %q", got)
	}
	if got := reportsOut(t, f, "", false, 20); !strings.Contains(got, "no new reports") {
		t.Fatalf("second read must be empty: %q", got)
	}

	// --all shows them again and marks nothing read.
	all := reportsOut(t, f, "", true, 20)
	if !strings.Contains(all, "nav fixed") || !strings.Contains(all, "need the schema") {
		t.Fatalf("--all: %q", all)
	}
	if n := f.UnreadReports(); n != 0 {
		t.Fatalf("--all must not change the unread count: %d", n)
	}
	if got := reportsOut(t, f, "", true, 1); strings.Contains(got, "nav fixed") || !strings.Contains(got, "need the schema") {
		t.Fatalf("--limit 1 must keep only the newest: %q", got)
	}

	// --as reads the same history as an agent: it shows the unread reports
	// but must not empty the admiral's queue.
	if _, err := f.Report("a", "progress", "tests next"); err != nil {
		t.Fatal(err)
	}
	if got := reportsOut(t, f, "a", false, 20); !strings.Contains(got, " a: progress: tests next\n") {
		t.Fatalf("--as: %q", got)
	}
	if n := f.UnreadReports(); n != 1 {
		t.Fatalf("--as must leave the report unread: %d", n)
	}
	if got := reportsOut(t, f, "", false, 20); !strings.Contains(got, "tests next") {
		t.Fatalf("the admiral must still get it: %q", got)
	}
	if n := f.UnreadReports(); n != 0 {
		t.Fatalf("the admiral's read must mark it: %d", n)
	}
	if err := writeReports(&bytes.Buffer{}, f, "nobody", false, 20); err == nil {
		t.Fatal("--as with an unknown agent must fail")
	}
}

// A flag or empty text is usage, not a report (issue #5).
func TestCmdReportRefusesFlagOrEmptyText(t *testing.T) {
	f := reportsHome(t)
	for _, args := range [][]string{
		{"--as", "a", "question", "--help"},
		{"--as", "a", "question", "-h"},
		{"--as", "a", "blocked", "-x", "y"},
		{"--as", "a", "done", " ", "\t"},
		{"--as", "a", "done"},
	} {
		err := cmdReport(f, args)
		if args[len(args)-1] == "--help" || args[len(args)-1] == "-h" {
			if err != nil {
				t.Fatalf("%q: help should print usage, got %v", args, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "usage: fleet report") {
			t.Fatalf("%q: want usage error, got %v", args, err)
		}
	}
	if err := cmdReport(f, []string{"--help"}); err != nil {
		t.Fatal(err)
	}
	if n := f.UnreadReports(); n != 0 {
		t.Fatalf("filed %d reports", n)
	}
	if err := cmdReport(f, []string{"--as", "a", "question", "which", "db?"}); err != nil || f.UnreadReports() != 1 {
		t.Fatalf("real report: %v", err)
	}
}
