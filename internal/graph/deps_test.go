package graph

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sturlabragason/fleet/internal/worktree"
)

// depsGraph: a.go calls b.go, b.go references c.go, a.go imports c's
// package (ignored), d_test.go calls a.go, and c.go calls into a test file
// (ignored: code never depends on a test).
const depsGraph = `{"nodes":[
 {"id":"a","source_file":"a.go","community":1},
 {"id":"a2","source_file":"a.go","community":1},
 {"id":"b","source_file":"b.go","community":1},
 {"id":"c","source_file":"c.go","community":2},
 {"id":"d","source_file":"d_test.go","community":1},
 {"id":"pkg","source_file":""}],
 "links":[
 {"source":"a","target":"b","relation":"calls"},
 {"source":"a","target":"a2","relation":"calls"},
 {"source":"b","target":"c","relation":"references"},
 {"source":"a","target":"c","relation":"imports_from"},
 {"source":"d","target":"a","relation":"calls"},
 {"source":"c","target":"d","relation":"calls"},
 {"source":"a","target":"pkg","relation":"calls"}]}`

func writeGraph(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "graph.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadDeps(t *testing.T) {
	d, err := LoadDeps(writeGraph(t, depsGraph))
	if err != nil {
		t.Fatal(err)
	}
	want := Deps{"a.go": {"b.go": true}, "b.go": {"c.go": true}, "d_test.go": {"a.go": true}}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("deps = %v, want %v", d, want)
	}
	if _, err := LoadDeps(t.TempDir()); err == nil {
		t.Fatal("no graph.json: want an error")
	}
}

func TestCouple(t *testing.T) {
	d := Deps{"a.go": {"b.go": true}, "b.go": {"c.go": true}, "d_test.go": {"a.go": true}}
	ws := []worktree.Worktree{
		{Path: "/w/same", Branch: "fleet/same", Files: []string{"a.go", "x.go"}},
		{Path: "/w/below", Branch: "fleet/below", Files: []string{"b.go"}},
		{Path: "/w/above", Branch: "fleet/above", Files: []string{"d_test.go"}},
		{Path: "/w/far", Branch: "fleet/far", Files: []string{"c.go", "z.go"}},
	}
	got := Couple(d, []string{"a.go"}, ws)
	want := []Coupling{
		{Worktree: ws[0], Overlap: []string{"a.go"}},
		{Worktree: ws[1], Uses: []Link{{From: "a.go", To: "b.go"}}, Order: "merge fleet/below first: your files use its changes"},
		{Worktree: ws[2], UsedBy: []Link{{From: "d_test.go", To: "a.go"}}, Order: "you merge first: its files use yours"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Couple =\n%+v\nwant\n%+v", got, want)
	}
	if got := Couple(d, []string{"z.go"}, ws[:3]); got != nil {
		t.Fatalf("unrelated file: want nil, got %+v", got)
	}
}

func TestCouplingText(t *testing.T) {
	cs := []Coupling{
		{Worktree: worktree.Worktree{Path: "/w/below", Branch: "fleet/below"}, Overlap: []string{"x.go"},
			Uses: []Link{{From: "a.go", To: "b.go"}}, UsedBy: []Link{{From: "c.go", To: "a.go"}}},
	}
	want := "/w/below  fleet/below\n" +
		"    both change x.go\n" +
		"    your a.go uses its b.go\n" +
		"    its c.go uses your a.go\n"
	if got := CouplingText(cs); got != want {
		t.Fatalf("text =\n%s\nwant\n%s", got, want)
	}
	if got := CouplingText(nil); got != "no other worktree changes these files or code they depend on\n" {
		t.Fatalf("empty text = %q", got)
	}
}
