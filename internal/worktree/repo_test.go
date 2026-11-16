package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoConfig(t *testing.T) {
	cases := []struct {
		name, toml string // toml "" writes no file
		check      func(t *testing.T, c RepoConfig)
		wantErr    string
	}{
		{name: "no file falls back", check: func(t *testing.T, c RepoConfig) {
			t.Helper()
			d := DefaultRepoConfig()
			if c.Found || c.MergeCheck != DefaultMergeCheck || c.ADRFile != "DESIGN.md" ||
				len(c.AppendOnly) != 2 || !c.AppendOnly["CHANGELOG.md"] || c.ADREntry.String() != d.ADREntry.String() {
				t.Fatalf("defaults: %+v", c)
			}
		}},
		{name: "other files honoured", toml: `merge_check = "just ci"
[[append_only]]
path = "NOTES.md"
order = "newest-first"
[[append_only]]
path = "docs/adr.md"
order = "oldest-first"
[adr]
file = "docs/adr.md"
entry = '(?m)^## (\d+)\.'
`, check: func(t *testing.T, c RepoConfig) {
			t.Helper()
			if !c.Found || c.MergeCheck != "just ci" || c.ADRFile != "docs/adr.md" {
				t.Fatalf("config: %+v", c)
			}
			if len(c.AppendOnly) != 2 || !c.AppendOnly["NOTES.md"] || c.AppendOnly["docs/adr.md"] {
				t.Fatalf("append_only: %v", c.AppendOnly)
			}
			if m := c.ADREntry.FindStringSubmatch("x\n## 7. seven\n"); len(m) != 2 || m[1] != "7" {
				t.Fatalf("entry match %q", m)
			}
		}},
		{name: "partial keeps defaults", toml: "merge_check = \"go test ./...\"\n", check: func(t *testing.T, c RepoConfig) {
			t.Helper()
			if c.MergeCheck != "go test ./..." || c.ADRFile != "DESIGN.md" || len(c.AppendOnly) != 2 {
				t.Fatalf("config: %+v", c)
			}
		}},
		{name: "header with a comment", toml: "[[append_only]] # ours on top\npath = \"A.md\"\norder = \"newest-first\"\n", check: func(t *testing.T, c RepoConfig) {
			t.Helper()
			if len(c.AppendOnly) != 1 || !c.AppendOnly["A.md"] {
				t.Fatalf("append_only: %v", c.AppendOnly)
			}
		}},
		{name: "unknown array table", toml: "[[shared]]\npath = \"A.md\"\n", wantErr: "only [[append_only]]"},
		{name: "bad order", toml: "[[append_only]]\npath = \"A.md\"\norder = \"up\"\n", wantErr: "order"},
		{name: "entry without a group", toml: "[adr]\nentry = '^ADR'\n", wantErr: "one group"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := testRepo(t)
			if tc.toml != "" {
				if err := os.MkdirAll(filepath.Join(repo, ".fleet"), 0o755); err != nil {
					t.Fatal(err)
				}
				write(t, repo, RepoConfigFile, tc.toml)
			}
			c, err := LoadRepoConfig(repo)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error with %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, c)
		})
	}
}

// A file the repo names append-only keeps both sides on a conflict, as
// CHANGELOG.md does in fleet's own layout.
func TestMergeHonoursRepoAppendOnly(t *testing.T) {
	repo, wts := mergeRepo(t, "a", "b")
	for _, n := range []string{"a", "b"} {
		write(t, wts[n], "code.go", "package code\n\nconst A = 1\n// "+n+"\n")
		run(t, wts[n], "commit", "-q", "-am", "feat: "+n)
	}
	only := map[string]bool{"code.go": true}
	if _, err := Merge(Options{Dir: wts["a"], AppendOnly: only}); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	if _, err := Merge(Options{Dir: wts["b"], AppendOnly: only}); err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if got, want := read(t, repo, "code.go"), "package code\n\nconst A = 1\n// b\n// a\n"; got != want {
		t.Fatalf("code.go:\n%q\nwant\n%q", got, want)
	}
}
