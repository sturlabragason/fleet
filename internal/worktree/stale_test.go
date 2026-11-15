package worktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func staleByName(es []Entry) map[string]Entry {
	m := map[string]Entry{}
	for _, e := range es {
		m[e.Name] = e
	}
	return m
}

// staleRepo makes: merged clean (a), unmerged commit (b), merged with an
// untracked file (c), merged with only a result.md (d), all under repo.wt/.
// The fleet home is a temp dir, so a prune archives there.
func staleRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("FLEET_CONFIG", filepath.Join(t.TempDir(), "none.toml"))
	t.Setenv("FLEET_HOME", t.TempDir())
	repo := testRepo(t)
	for _, n := range []string{"a", "b", "c", "d"} {
		run(t, repo, "worktree", "add", "-q", "-b", "fleet/"+n, Path(repo, n))
	}
	b := Path(repo, "b")
	os.WriteFile(filepath.Join(b, "new.go"), []byte("x\n"), 0o644)
	run(t, b, "add", ".")
	run(t, b, "commit", "-q", "-m", "work")
	os.WriteFile(filepath.Join(Path(repo, "c"), "scratch.txt"), []byte("x\n"), 0o644)
	os.WriteFile(filepath.Join(Path(repo, "d"), "result.md"), []byte("x\n"), 0o644)
	return repo
}

func TestStaleListAndPrune(t *testing.T) {
	repo := staleRepo(t)
	main, _ := Main(repo)
	es, err := Stale(main, repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := staleByName(es)
	if len(m) != 4 {
		t.Fatalf("got %d entries, want 4: %+v", len(es), es)
	}
	if a := m["a"]; a.Merged == nil || !*a.Merged || a.Dirty || !a.Prunable || a.Bytes == 0 {
		t.Errorf("a: %+v", a)
	}
	if b := m["b"]; b.Merged == nil || *b.Merged || b.Dirty || b.Prunable {
		t.Errorf("b: %+v", b)
	}
	if c := m["c"]; c.Merged == nil || !*c.Merged || !c.Dirty || c.Prunable || c.ResultOnly {
		t.Errorf("c: %+v", c)
	}
	if d := m["d"]; !d.Dirty || !d.ResultOnly || !d.Prunable {
		t.Errorf("d: %+v", d)
	}
	rs := Prune(main, es)
	if len(rs) != 2 || !rs[0].Removed || rs[0].Branch != "fleet/a" || !rs[1].Removed {
		t.Fatalf("prune: %+v", rs)
	}
	if _, err := os.Stat(Path(repo, "a")); !os.IsNotExist(err) {
		t.Error("a still on disk")
	}
	for _, n := range []string{"b", "c"} {
		if _, err := os.Stat(Path(repo, n)); err != nil {
			t.Errorf("%s removed: %v", n, err)
		}
	}
	if _, err := os.Stat(filepath.Join(Path(repo, "c"), "scratch.txt")); err != nil {
		t.Error("c's untracked file lost")
	}
	if _, err := gitCtxT(main, "rev-parse", "--verify", "--quiet", "refs/heads/fleet/a"); err == nil {
		t.Error("branch fleet/a not deleted")
	}
}

func gitCtxT(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), staleTimeout)
	defer cancel()
	return gitCtx(ctx, dir, args...)
}

func TestStaleHeldAndCwdNotPrunable(t *testing.T) {
	repo := staleRepo(t)
	main, _ := Main(repo)
	es, _ := Stale(main, Path(repo, "a"), nil)
	if staleByName(es)["a"].Prunable {
		t.Error("the cwd's worktree is prunable")
	}
	for _, st := range []string{"live", "parked"} {
		es, _ = Stale(main, repo, map[string]string{Path(repo, "a"): st})
		if a := staleByName(es)["a"]; a.Prunable || a.Agent != st {
			t.Errorf("held %s: %+v", st, a)
		}
	}
	// A closed agent's merged worktree can go: its work is in main.
	es, _ = Stale(main, repo, map[string]string{Path(repo, "a"): "closed"})
	if a := staleByName(es)["a"]; !a.Prunable || a.Agent != "closed" {
		t.Errorf("held closed: %+v", a)
	}
}

func TestStaleSkipsOutsideWt(t *testing.T) {
	repo := staleRepo(t)
	main, _ := Main(repo)
	out := filepath.Join(t.TempDir(), "elsewhere")
	run(t, repo, "worktree", "add", "-q", "-b", "other", out)
	es, _ := Stale(main, repo, nil)
	if _, ok := staleByName(es)["elsewhere"]; ok {
		t.Error("worktree outside repo.wt listed")
	}
}

func TestStaleGone(t *testing.T) {
	repo := staleRepo(t)
	main, _ := Main(repo)
	os.RemoveAll(Path(repo, "a"))
	es, _ := Stale(main, repo, nil)
	a := staleByName(es)["a"]
	if !a.Gone || !a.Prunable {
		t.Fatalf("a: %+v", a)
	}
	if rs := Prune(main, es); len(rs) == 0 || !rs[0].Entry.Gone || !rs[0].Removed {
		t.Fatalf("prune: %+v", rs)
	}
	es, _ = Stale(main, repo, nil)
	if _, ok := staleByName(es)["a"]; ok {
		t.Error("registration still listed")
	}
}

func TestHumanSize(t *testing.T) {
	for n, want := range map[int64]string{0: "0B", 2048: "2.0K", 412 << 20: "412M", 3 << 30: "3.0G"} {
		if got := HumanSize(n); got != want {
			t.Errorf("%d: %s, want %s", n, got, want)
		}
	}
}

// patchRepo makes p, whose one commit reached main by cherry-pick (a new
// sha, so p is not an ancestor of main), and x, the same commit plus one
// real commit of its own.
func patchRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("FLEET_CONFIG", filepath.Join(t.TempDir(), "none.toml"))
	t.Setenv("FLEET_HOME", t.TempDir())
	repo := testRepo(t)
	p := Path(repo, "p")
	run(t, repo, "worktree", "add", "-q", "-b", "fleet/p", p)
	os.WriteFile(filepath.Join(p, "p.go"), []byte("p\n"), 0o644)
	run(t, p, "add", ".")
	run(t, p, "commit", "-q", "-m", "p work")
	os.WriteFile(filepath.Join(repo, "m.go"), []byte("m\n"), 0o644)
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-q", "-m", "main moved")
	run(t, repo, "cherry-pick", "fleet/p")
	x := Path(repo, "x")
	run(t, repo, "worktree", "add", "-q", "-b", "fleet/x", x, "fleet/p")
	os.WriteFile(filepath.Join(x, "x.go"), []byte("x\n"), 0o644)
	run(t, x, "add", ".")
	run(t, x, "commit", "-q", "-m", "x work")
	return repo
}

// A branch whose commits are in main by patch id reads as merged and is
// pruned with its branch; one with a real commit of its own is not.
func TestStaleMergedByPatchID(t *testing.T) {
	repo := patchRepo(t)
	main, _ := Main(repo)
	es, err := Stale(main, repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := staleByName(es)
	if p := m["p"]; p.Merged == nil || !*p.Merged || !p.Prunable {
		t.Errorf("p: %+v", p)
	}
	if x := m["x"]; x.Merged == nil || *x.Merged || x.Prunable {
		t.Errorf("x: %+v", x)
	}
	rs := Prune(main, es)
	if len(rs) != 1 || !rs[0].Removed || rs[0].Branch != "fleet/p" {
		t.Fatalf("prune: %+v", rs)
	}
	if _, err := os.Stat(Path(repo, "x")); err != nil {
		t.Error("x removed")
	}
}

// A merged worktree whose only change is an untracked result file is pruned
// after the file is copied to <fleet home>/results/<repo>/<worktree>/, never
// over an earlier copy; any other untracked file still refuses.
func TestPruneArchivesResultFile(t *testing.T) {
	repo := staleRepo(t)
	home := t.TempDir()
	t.Setenv("FLEET_HOME", home)
	main, _ := Main(repo)
	want := []byte("the handoff\n")
	os.WriteFile(filepath.Join(Path(repo, "d"), "result.md"), want, 0o644)
	os.WriteFile(filepath.Join(Path(repo, "d"), "result-d.md"), want, 0o644)
	dest := filepath.Join(home, "results", filepath.Base(main), "d")
	os.MkdirAll(dest, 0o755)
	os.WriteFile(filepath.Join(dest, "result.md"), []byte("older\n"), 0o644)
	es, err := Stale(main, repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := staleByName(es)
	if d := m["d"]; !d.ResultOnly || !d.Prunable {
		t.Fatalf("d: %+v", d)
	}
	if c := m["c"]; c.Prunable {
		t.Errorf("c with scratch.txt is prunable: %+v", c)
	}
	rs := Prune(main, es)
	var gotD bool
	for _, r := range rs {
		if r.Entry.Name == "d" {
			gotD = r.Removed && len(r.Archived) == 2
		}
	}
	if !gotD {
		t.Fatalf("prune: %+v", rs)
	}
	if _, err := os.Stat(Path(repo, "d")); !os.IsNotExist(err) {
		t.Error("d still on disk")
	}
	for _, f := range []string{"result-1.md", "result-d.md"} {
		if got, err := os.ReadFile(filepath.Join(dest, f)); err != nil || string(got) != string(want) {
			t.Errorf("%s: %q %v", f, got, err)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "result.md")); string(got) != "older\n" {
		t.Errorf("earlier copy overwritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(Path(repo, "c"), "scratch.txt")); err != nil {
		t.Error("c's untracked file lost")
	}
	// A card's result name that doesn't match result*.md counts when given.
	es, _ = Stale(main, repo, nil, filepath.Join(Path(repo, "c"), "scratch.txt"))
	if c := staleByName(es)["c"]; !c.ResultOnly || !c.Prunable {
		t.Errorf("c with scratch.txt as its card result: %+v", c)
	}
}
