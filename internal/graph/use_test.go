package graph

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// freshGraph writes a graph for repo at its HEAD: m.go calls internal/x/a.go.
func freshGraph(t *testing.T, home, repo string) {
	t.Helper()
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	out := Dir(home, repo)
	write(t, filepath.Join(out, stampFile), strings.Replace(goodStamp(repo), `"abc"`, `"`+strings.TrimSpace(string(head))+`"`, 1))
	write(t, filepath.Join(out, "graph.json"), `{"nodes":[
 {"id":"1","label":"A()","source_file":"internal/x/a.go","file_type":"code"},
 {"id":"2","label":"M()","source_file":"m.go","file_type":"code"}],
 "links":[{"source":"2","target":"1","relation":"calls"}]}`)
}

func TestFreshAndAuto(t *testing.T) {
	repo, wt := gitRepo(t)
	home := t.TempDir()
	yes, no := true, false
	if _, err := Fresh(t.Context(), home, wt); !errors.Is(err, ErrNoGraph) {
		t.Errorf("no graph: %v", err)
	}
	if use, note := Auto(t.Context(), home, wt, []string{"a.go"}, nil); use || note != "" {
		t.Errorf("no graph: Auto = %v %q, want silent fallback", use, note)
	}
	out := Dir(home, repo)
	write(t, filepath.Join(out, stampFile), goodStamp(repo)) // commit "abc" is unknown
	write(t, filepath.Join(out, "graph.json"), "{}")
	if _, err := Fresh(t.Context(), home, wt); !errors.Is(err, ErrStale) {
		t.Errorf("unknown commit: %v, want stale", err)
	}
	if use, note := Auto(t.Context(), home, wt, []string{"a.go"}, nil); use || !strings.Contains(note, "file overlap only") {
		t.Errorf("stale: Auto = %v %q", use, note)
	}
	freshGraph(t, home, repo)
	if got, err := Fresh(t.Context(), home, wt); err != nil || got != out {
		t.Errorf("fresh: %q %v", got, err)
	}
	cases := []struct {
		files []string
		asked *bool
		want  bool
	}{
		{[]string{"a.go"}, nil, true},
		{nil, nil, false}, // the overview keeps its plain listing
		{[]string{"a.go"}, &no, false},
		{nil, &yes, true},
	}
	for _, c := range cases {
		if use, _ := Auto(t.Context(), home, wt, c.files, c.asked); use != c.want {
			t.Errorf("Auto(%v, %v) = %v, want %v", c.files, c.asked, use, c.want)
		}
	}
}

func TestBrief(t *testing.T) {
	repo, wt := gitRepo(t)
	home := t.TempDir()
	write(t, filepath.Join(repo, "internal", "x", "a.go"), "package x")
	if got := Brief(t.Context(), home, wt, "fix `internal/x/a.go` please"); got != "" {
		t.Errorf("no graph: %q", got)
	}
	freshGraph(t, home, repo)
	for _, p := range []string{"fix `internal/x/a.go`, please", "look in internal/x/"} {
		got := Brief(t.Context(), home, wt, p)
		if !strings.Contains(got, "is used by m.go\n") {
			t.Errorf("Brief(%q) = %q", p, got)
		}
	}
	for _, p := range []string{"no paths here", "/etc/passwd and ../x/y", "m.go/nothing"} {
		if got := Brief(t.Context(), home, wt, p); got != "" {
			t.Errorf("Brief(%q) = %q, want empty", p, got)
		}
	}
	if n := Reads(home, time.Time{})["brief"]; n != 2 {
		t.Errorf("brief reads = %d, want 2", n)
	}
	if n := len(Reads(home, time.Now().Add(time.Hour))); n != 0 {
		t.Errorf("reads after now = %d", n)
	}
}
