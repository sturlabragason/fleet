package graph

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/worktree"
)

// depsGraph: a.go calls b.go, b.go references c.go, a.go imports c's
// package (ignored), d_test.go calls a.go, and c.go calls into a test file
// (ignored: code never depends on a test). DESIGN.md names a.go (ignored:
// a document is not a dependency).
const depsGraph = `{"nodes":[
 {"id":"a","source_file":"a.go","community":1},
 {"id":"a2","source_file":"a.go","community":1},
 {"id":"b","source_file":"b.go","community":1},
 {"id":"c","source_file":"c.go","community":2},
 {"id":"d","source_file":"d_test.go","community":1},
 {"id":"pkg","source_file":""},
 {"id":"m","source_file":"DESIGN.md","file_type":"document"}],
 "links":[
 {"source":"a","target":"b","relation":"calls"},
 {"source":"a","target":"a2","relation":"calls"},
 {"source":"b","target":"c","relation":"references"},
 {"source":"a","target":"c","relation":"imports_from"},
 {"source":"d","target":"a","relation":"calls"},
 {"source":"c","target":"d","relation":"calls"},
 {"source":"a","target":"pkg","relation":"calls"},
 {"source":"m","target":"a","relation":"references"}]}`

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

func TestFiles(t *testing.T) {
	out := writeGraph(t, `{"nodes":[
 {"id":"1","label":"Touching()","source_file":"internal/worktree/touching.go","file_type":"code"},
 {"id":"2","label":"Changes()","source_file":"internal/worktree/worktree.go","file_type":"code"},
 {"id":"3","label":".Changes()","source_file":"internal/ui/backend.go","file_type":"code"},
 {"id":"4","label":"Model","source_file":"internal/ui/ui.go","file_type":"code"},
 {"id":"5","label":"Changes","source_file":"DESIGN.md","file_type":"document"}],"links":[]}`)
	cases := []struct {
		name string
		want []string
	}{
		{"Touching", []string{"internal/worktree/touching.go"}},
		{"Changes", []string{"internal/ui/backend.go", "internal/worktree/worktree.go"}},
		{"worktree.Changes", []string{"internal/worktree/worktree.go"}},
		{"ui.Model", []string{"internal/ui/ui.go"}},
		{"Backend.Changes", []string{"internal/ui/backend.go"}}, // Type.Method: methods only
		{"touching.go", []string{"internal/worktree/touching.go"}},
		{"internal/worktree", []string{"internal/worktree/touching.go", "internal/worktree/worktree.go"}},
		{"internal/ui/ui.go", []string{"internal/ui/ui.go"}},
		{"internal/work", nil},
		{"Nope", nil},
	}
	g, err := load(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if got := g.files(c.name); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Files(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestResolve(t *testing.T) {
	repo, wt := gitRepo(t)
	home := t.TempDir()
	write(t, filepath.Join(repo, "docs", "a.md"), "x")
	if l := Resolve(home, wt, []string{"docs/a.md", "Touching", "merge"}); l.Root != repo || !reflect.DeepEqual(l.Files, []string{"docs/a.md"}) || l.UsedBy != nil {
		t.Errorf("no graph: %+v, want only the path that exists", l)
	}
	out := Dir(home, repo)
	write(t, filepath.Join(out, stampFile), goodStamp(repo))
	write(t, filepath.Join(out, "graph.json"), `{"nodes":[
 {"id":"1","label":"Touching()","source_file":"t.go","file_type":"code"},
 {"id":"2","label":"merge()","source_file":"m.go","file_type":"code"}],
 "links":[{"source":"2","target":"1","relation":"calls"}]}`)
	cases := []struct {
		words      []string
		want, used []string
	}{
		{[]string{"who", "touched", "Touching"}, []string{"t.go"}, []string{"m.go"}},
		{[]string{"merge"}, nil, nil}, // a plain word is not taken as code
		{[]string{"Touching", "t.go"}, []string{"t.go"}, []string{"m.go"}},
		{[]string{"docs/a.md"}, []string{"docs/a.md"}, nil}, // a path the graph lacks
	}
	for _, c := range cases {
		if l := Resolve(home, wt, c.words); !reflect.DeepEqual(l.Files, c.want) || !reflect.DeepEqual(l.UsedBy, c.used) {
			t.Errorf("Resolve(%v) = %v used by %v, want %v used by %v", c.words, l.Files, l.UsedBy, c.want, c.used)
		}
	}
	if n := Reads(home, time.Time{})["recall"]; n != 3 {
		t.Errorf("recall reads = %d, want 3 (one per lookup with a code word)", n)
	}
	if l := Resolve(home, t.TempDir(), []string{"x.go"}); l.Root != "" || l.Files != nil {
		t.Errorf("outside a repo: %+v", l)
	}
}

func TestUsedBy(t *testing.T) {
	d := Deps{"a.go": {"b.go": true}, "b.go": {"c.go": true}, "d_test.go": {"a.go": true}}
	cases := []struct {
		files, want []string
	}{
		{[]string{"b.go"}, []string{"a.go"}},
		{[]string{"a.go", "b.go"}, []string{"d_test.go"}}, // a user inside the set is left out
		{[]string{"d_test.go"}, nil},
	}
	for _, c := range cases {
		if got := d.UsedBy(c.files); !reflect.DeepEqual(got, c.want) {
			t.Errorf("UsedBy(%v) = %v, want %v", c.files, got, c.want)
		}
	}
}

func TestCodeWord(t *testing.T) {
	for w, want := range map[string]bool{
		"internal/graph": true, "deps.go": true, "graph.Resolve": true, "Resolve": true, "./internal/x": true,
		"github.com/owner/repo": false, "https://example.com/a": false, "theme": false, "": false,
	} {
		if got := codeWord(w); got != want {
			t.Errorf("codeWord(%q) = %v, want %v", w, got, want)
		}
	}
}
