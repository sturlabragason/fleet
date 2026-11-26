package graph

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/sysx"
	"github.com/sturlabragason/fleet/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestKeyDir(t *testing.T) {
	if got := Key("/home/u/git/x"); got != "home-u-git-x" {
		t.Errorf("Key = %q", got)
	}
	if got := Key("/"); got != "root" {
		t.Errorf("Key(/) = %q", got)
	}
	if got := Dir("/h", "/a/b"); got != "/h/graphs/a-b" {
		t.Errorf("Dir = %q", got)
	}
}

func gitRepo(t *testing.T) (repo, wt string) {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	repo = filepath.Join(base, "repo")
	wt = filepath.Join(base, "repo.wt", "a")
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.MkdirAll(repo, 0o755)
	run(repo, "init", "-q", "-b", "main")
	run(repo, "commit", "-q", "--allow-empty", "-m", "x")
	run(repo, "worktree", "add", "-q", "-b", "a", wt)
	return repo, wt
}

func TestRoot(t *testing.T) {
	repo, wt := gitRepo(t)
	for _, d := range []string{repo, wt} {
		got, err := Root(d)
		if err != nil || got != repo {
			t.Errorf("Root(%s) = %q, %v; want %q", d, got, err, repo)
		}
	}
	if _, err := Root(t.TempDir()); err == nil {
		t.Error("Root outside a repo should fail")
	}
}

func goodStamp(root string) string {
	return `{"root":"` + root + `","built_at":"2026-01-02T03:04:05Z","commit":"abc","schema_version":1,"nodes":2,"edges":1}`
}

func TestFind(t *testing.T) {
	repo, wt := gitRepo(t)
	home := t.TempDir()
	if _, _, ok := Find(home, wt); ok {
		t.Fatal("found with no stamp")
	}
	out := Dir(home, repo)
	write(t, filepath.Join(out, stampFile), strings.Replace(goodStamp(repo), `"schema_version":1`, `"schema_version":2`, 1))
	write(t, filepath.Join(out, "graph.json"), "{}")
	if _, _, ok := Find(home, wt); ok {
		t.Fatal("found schema 2")
	}
	write(t, filepath.Join(out, stampFile), goodStamp(repo))
	os.Remove(filepath.Join(out, "graph.json"))
	if _, _, ok := Find(home, wt); ok {
		t.Fatal("found without graph.json")
	}
	write(t, filepath.Join(out, "graph.json"), "")
	st, got, ok := Find(home, wt)
	if !ok || got != out || st.Nodes != 2 || st.Commit != "abc" {
		t.Fatalf("Find = %+v %q %v", st, got, ok)
	}
	if n := len(List(home)); n != 1 {
		t.Errorf("List = %d", n)
	}
}

// fakeGraphify swaps the runner for one that writes a small graph.json into
// GRAPHIFY_OUT, and points the companion at a stand-in binary.
func fakeGraphify(t *testing.T, run func(out string) error) *[][]string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "graphify")
	write(t, bin, "#!/bin/sh\n")
	os.Chmod(bin, 0o755)
	t.Setenv("FLEET_GRAPHIFY", bin)
	t.Setenv("FLEET_CONFIG", filepath.Join(t.TempDir(), "none.toml"))
	t.Setenv("FLEET_FLOORPLAN", "off")
	var calls [][]string
	old := companion.Runner
	companion.Runner = func(_ context.Context, _ string, args []string, o companion.Opts) ([]byte, error) {
		if len(args) > 0 && args[0] == "--version" {
			return []byte("graphify 0.9.109"), nil
		}
		calls = append(calls, args)
		if !o.Clean || o.Timeout != 5*time.Minute || !o.Low {
			t.Errorf("opts = %+v", o)
		}
		var out string
		for _, e := range o.Env {
			if v, ok := strings.CutPrefix(e, "GRAPHIFY_OUT="); ok {
				out = v
			}
		}
		return nil, run(out)
	}
	t.Cleanup(func() { companion.Runner = old })
	return &calls
}

const smallGraph = `{"graph":{"schema_version":1,"graphify_version":"0.9.109"},"nodes":[{},{},{}],"links":[{},{}],"built_at_commit":"deadbeef"}`

func TestBuild(t *testing.T) {
	home := t.TempDir()
	root := "/r/x"
	calls := fakeGraphify(t, func(out string) error {
		write(t, filepath.Join(out, "graph.json"), smallGraph)
		return nil
	})
	st, err := Build(context.Background(), home, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if st.Nodes != 3 || st.Edges != 2 || st.Commit != "deadbeef" || st.GraphifyVersion != "0.9.109" || st.Root != root {
		t.Errorf("stamp = %+v", st)
	}
	if len(*calls) != 1 || strings.Join((*calls)[0], " ") != "update /r/x --force" {
		t.Errorf("calls = %v", *calls)
	}
	got, ok := readStamp(Dir(home, root))
	if !ok || got.Nodes != 3 {
		t.Errorf("stamp on disk = %+v %v", got, ok)
	}
}

func TestBuildFailureKeepsStamp(t *testing.T) {
	home := t.TempDir()
	root := "/r/x"
	write(t, filepath.Join(Dir(home, root), stampFile), goodStamp(root))
	fakeGraphify(t, func(string) error { return errors.New("boom") })
	if _, err := Build(context.Background(), home, root, false); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if st, ok := readStamp(Dir(home, root)); !ok || st.Nodes != 2 {
		t.Errorf("stamp changed: %+v %v", st, ok)
	}
}

func TestBuildTooNew(t *testing.T) {
	home := t.TempDir()
	fakeGraphify(t, func(out string) error {
		write(t, filepath.Join(out, "graph.json"), strings.Replace(smallGraph, `"schema_version":1`, `"schema_version":2`, 1))
		return nil
	})
	_, err := Build(context.Background(), home, "/r/x", false)
	if err == nil || !strings.Contains(err.Error(), "graphify too new") {
		t.Fatalf("err = %v", err)
	}
	if _, ok := readStamp(Dir(home, "/r/x")); ok {
		t.Error("stamp written for a too-new graph")
	}
}

func TestBuildBusyAndRerun(t *testing.T) {
	home := t.TempDir()
	root := "/r/x"
	calls := fakeGraphify(t, func(out string) error {
		write(t, filepath.Join(out, "graph.json"), smallGraph)
		return nil
	})
	gdir := filepath.Join(home, "graphs")
	os.MkdirAll(gdir, 0o755)
	l, _ := os.OpenFile(filepath.Join(gdir, Key(root)+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	defer l.Close()
	if err := sysx.Flock(l, sysx.LockEx); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(context.Background(), home, root, false); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if _, err := os.Stat(filepath.Join(gdir, Key(root)+".rerun")); err != nil {
		t.Errorf("rerun marker missing: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("ran while locked: %v", *calls)
	}
	// A marker dropped mid-build makes it go round once more.
	sysx.Flock(l, sysx.LockUn)
	n := 0
	fakeGraphify(t, func(out string) error {
		write(t, filepath.Join(out, "graph.json"), smallGraph)
		if n++; n == 1 {
			write(t, filepath.Join(gdir, Key(root)+".rerun"), "")
		}
		return nil
	})
	if _, err := Build(context.Background(), home, root, false); err != nil || n != 2 {
		t.Fatalf("err = %v, runs = %d, want 2", err, n)
	}
}

func TestBuildMode(t *testing.T) {
	dir := t.TempDir()
	for want, body := range map[string]string{
		"merge":  "",
		"manual": "[integrations.graphify]\nbuild = \"manual\"\n",
	} {
		cfg := filepath.Join(dir, want+".toml")
		write(t, cfg, body)
		t.Setenv("FLEET_CONFIG", cfg)
		p, err := prefs.Load()
		if err != nil {
			t.Fatal(err)
		}
		if got := BuildMode(p); got != want {
			t.Errorf("BuildMode(%q) = %q, want %q", body, got, want)
		}
	}
	cfg := filepath.Join(dir, "bad.toml")
	write(t, cfg, "[integrations.graphify]\nbuild = \"always\"\n")
	t.Setenv("FLEET_CONFIG", cfg)
	p, _ := prefs.Load()
	if got := BuildMode(p); got != "merge" {
		t.Errorf("bad value = %q", got)
	}
	if BuildMode(nil) != "merge" {
		t.Error("nil prefs should be merge")
	}
}

func TestBuildRerunCapDropsMarker(t *testing.T) {
	home := t.TempDir()
	root := "/r/x"
	rerun := filepath.Join(home, "graphs", Key(root)+".rerun")
	calls := fakeGraphify(t, func(out string) error {
		write(t, filepath.Join(out, "graph.json"), smallGraph)
		write(t, rerun, "")
		return nil
	})
	if _, err := Build(context.Background(), home, root, false); err != nil {
		t.Fatal(err)
	}
	if n := len(*calls); n != buildPasses*buildPasses {
		t.Errorf("builds = %d, want %d", n, buildPasses*buildPasses)
	}
	if exists(rerun) {
		t.Error("rerun marker left after the cap")
	}
}
