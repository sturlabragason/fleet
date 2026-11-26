package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestQueue(t *testing.T) {
	// The fake merge_check fails when the tree holds a file named in bad, or
	// every file of a pair (two branches fine alone, broken together).
	tests := []struct {
		name        string
		branches    []string
		bad         []string
		pair        []string
		conflict    []string // these edit code.go: all but the first conflict
		wantMerged  []string
		wantEjected []string
	}{
		{name: "all green in one check", branches: []string{"a", "b", "c"}, wantMerged: []string{"a", "b", "c"}},
		{name: "bisect finds one culprit", branches: []string{"a", "b", "c", "d"}, bad: []string{"c"},
			wantMerged: []string{"a", "b", "d"}, wantEjected: []string{"c"}},
		{name: "two culprits", branches: []string{"a", "b", "c", "d", "e"}, bad: []string{"a", "e"},
			wantMerged: []string{"b", "c", "d"}, wantEjected: []string{"a", "e"}},
		{name: "broken only together", branches: []string{"a", "b", "c", "d"}, pair: []string{"a", "d"},
			wantMerged: []string{"a", "b", "c"}, wantEjected: []string{"d"}},
		{name: "conflict ejects, others land", branches: []string{"a", "b", "c"}, conflict: []string{"a", "b"},
			wantMerged: []string{"a", "c"}, wantEjected: []string{"b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, wts := mergeRepo(t, tt.branches...)
			var items []QueueItem
			for _, n := range tt.branches {
				write(t, wts[n], "CHANGELOG.md", "# What's new\n\n## 2026-11-05\n- from "+n+"\n- base line\n")
				write(t, wts[n], n+".src", n+"\n")
				if slices.Contains(tt.conflict, n) {
					write(t, wts[n], "code.go", "package code\n\nconst A = \""+n+"\"\n")
				}
				run(t, wts[n], "add", ".")
				run(t, wts[n], "commit", "-q", "-m", "feat: "+n)
				head, _ := revParse(wts[n], "HEAD")
				items = append(items, QueueItem{Branch: "fleet/" + n, Head: head})
			}
			checks := 0
			check := func(dir string) error {
				checks++
				has := func(n string) bool { _, err := os.Stat(filepath.Join(dir, n+".src")); return err == nil }
				for _, n := range tt.bad {
					if has(n) {
						return errors.New("FAIL: " + n + " is broken")
					}
				}
				if len(tt.pair) == 2 && has(tt.pair[0]) && has(tt.pair[1]) {
					return errors.New("FAIL: pair")
				}
				return nil
			}
			res, err := Queue(QueueOptions{Dir: repo, Items: items, Check: check, Log: func(s string) { t.Log(s) }})
			if err != nil {
				t.Fatal(err)
			}
			want := prefixed(tt.wantMerged)
			if !slices.Equal(res.Merged, want) {
				t.Fatalf("merged %v, want %v (ejected %+v)", res.Merged, want, res.Ejected)
			}
			var ej []string
			for _, e := range res.Ejected {
				ej = append(ej, e.Branch)
				if e.Reason == "" {
					t.Errorf("%s ejected without a reason", e.Branch)
				}
			}
			slices.Sort(ej)
			if !slices.Equal(ej, prefixed(tt.wantEjected)) {
				t.Fatalf("ejected %v, want %v", ej, tt.wantEjected)
			}
			if len(tt.bad)+len(tt.pair) == 0 && checks != 1 {
				t.Errorf("a green batch ran the check %d times, want once", checks)
			}
			checkQueueRepo(t, repo, wts, items, tt.branches, tt.wantMerged, tt.wantEjected)
		})
	}
}

// checkQueueRepo checks main and the branch refs after a queue run: landed
// work on main with its CHANGELOG line, landed refs merged, ejected refs
// untouched, no scratch worktree left.
func checkQueueRepo(t *testing.T, repo string, wts map[string]string, items []QueueItem, branches, wantMerged, wantEjected []string) {
	t.Helper()
	for _, n := range branches {
		_, err := os.Stat(filepath.Join(repo, n+".src"))
		if merged := slices.Contains(wantMerged, n); merged != (err == nil) {
			t.Errorf("%s.src on main: %v, want %v", n, err == nil, merged)
		}
	}
	cl := read(t, repo, "CHANGELOG.md")
	for _, n := range wantMerged {
		if !strings.Contains(cl, "- from "+n+"\n") {
			t.Errorf("CHANGELOG lost %s's line:\n%s", n, cl)
		}
		// The landed branch now points at main's commits, as after fleet merge.
		if !Merged(repo, "fleet/"+n) || !Merged(wts[n], "HEAD") {
			t.Errorf("fleet/%s not seen as merged", n)
		}
	}
	for _, e := range wantEjected {
		if head, _ := revParse(repo, "fleet/"+e); head != itemHead(items, "fleet/"+e) {
			t.Errorf("ejected fleet/%s was moved", e)
		}
	}
	if out, _ := git(repo, "worktree", "list"); strings.Contains(out, "fleet-queue-") {
		t.Errorf("scratch worktree left behind:\n%s", out)
	}
}

func TestOrderQueue(t *testing.T) {
	repo, wts := mergeRepo(t, "big", "small", "also")
	for n, files := range map[string]int{"big": 3, "small": 1, "also": 1} {
		for i := range files {
			write(t, wts[n], n+string(rune('a'+i))+".go", "package x\n")
		}
		run(t, wts[n], "add", ".")
		run(t, wts[n], "commit", "-q", "-m", n)
	}
	items, err := OrderQueue(repo, []string{"fleet/big", "fleet/small", "fleet/also"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.Branch)
	}
	if want := []string{"fleet/also", "fleet/small", "fleet/big"}; !slices.Equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}

func prefixed(ns []string) []string {
	out := []string{}
	for _, n := range ns {
		out = append(out, "fleet/"+n)
	}
	slices.Sort(out)
	return out
}

func itemHead(items []QueueItem, b string) string {
	for _, it := range items {
		if it.Branch == b {
			return it.Head
		}
	}
	return ""
}
