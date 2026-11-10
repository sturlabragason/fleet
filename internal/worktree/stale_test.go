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
func staleRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("FLEET_CONFIG", filepath.Join(t.TempDir(), "none.toml"))
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
	if d := m["d"]; !d.Dirty || !d.ResultOnly || d.Prunable {
		t.Errorf("d: %+v", d)
	}
	rs := Prune(main, es)
	if len(rs) != 1 || !rs[0].Removed || rs[0].Branch != "fleet/a" {
		t.Fatalf("prune: %+v", rs)
	}
	if _, err := os.Stat(Path(repo, "a")); !os.IsNotExist(err) {
		t.Error("a still on disk")
	}
	for _, n := range []string{"b", "c", "d"} {
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
	if rs := Prune(main, es); len(rs) != 1 || !rs[0].Removed {
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
