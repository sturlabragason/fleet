package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// remoteRepo makes a repo with a local bare origin and pushes main to it.
func remoteRepo(t *testing.T) (repo, bare string) {
	t.Helper()
	repo = testRepo(t)
	bare = filepath.Join(t.TempDir(), "origin.git")
	run(t, repo, "init", "-q", "--bare", bare)
	run(t, repo, "remote", "add", Remote, bare)
	run(t, repo, "push", "-q", "-u", Remote, "main")
	return repo, bare
}

// pushBranch commits file on a new branch off main and pushes it.
func pushBranch(t *testing.T, repo, branch, file string) {
	t.Helper()
	run(t, repo, "checkout", "-q", "-b", branch, "main")
	os.WriteFile(filepath.Join(repo, file), []byte(branch+"\n"), 0o644)
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-q", "-m", branch)
	run(t, repo, "push", "-q", Remote, branch)
	run(t, repo, "checkout", "-q", "main")
}

// rebaseMerge lands branch on main the way `fleet merge` does after main
// moved: as a new commit with the same patch, so the pushed copy is not an
// ancestor of main.
func rebaseMerge(t *testing.T, repo, branch string) {
	t.Helper()
	run(t, repo, "cherry-pick", branch)
}

func remoteHeads(t *testing.T, repo string) string {
	t.Helper()
	out, err := gitCtxT(repo, "ls-remote", "--heads", Remote)
	if err != nil {
		t.Fatal(err)
	}
	var hs []string
	for _, f := range strings.Fields(out) {
		if h, ok := strings.CutPrefix(f, "refs/heads/"); ok {
			hs = append(hs, h)
		}
	}
	return strings.Join(hs, " ")
}

func TestRemoteBranchesPrune(t *testing.T) {
	repo, bare := remoteRepo(t)
	os.WriteFile(filepath.Join(repo, "m.go"), []byte("m\n"), 0o644)
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-q", "-m", "main moves on")
	for _, b := range []string{"fleet/done", "fleet/live", "fleet/open", "fleet/moved"} {
		pushBranch(t, repo, b, strings.TrimPrefix(b, "fleet/")+".go")
	}
	for _, b := range []string{"fleet/done", "fleet/live", "fleet/moved"} {
		rebaseMerge(t, repo, b)
	}
	bs, err := RemoteBranches(repo, map[string]string{"fleet/live": "live"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]RemoteBranch{}
	for _, b := range bs {
		got[b.Branch] = b
	}
	want := map[string]bool{"fleet/done": true, "fleet/live": false, "fleet/open": false, "fleet/moved": true}
	for b, p := range want {
		if got[b].Prunable != p {
			t.Errorf("%s prunable = %v, want %v (%+v)", b, got[b].Prunable, p, got[b])
		}
	}
	if !got["fleet/live"].Merged || got["fleet/open"].Merged {
		t.Errorf("merged flags wrong: %+v", got)
	}
	// Someone pushes to fleet/moved after the check: the lease refuses it.
	other := filepath.Join(t.TempDir(), "other")
	run(t, repo, "clone", "-q", "-b", "fleet/moved", bare, other)
	os.WriteFile(filepath.Join(other, "late.go"), []byte("late\n"), 0o644)
	run(t, other, "add", ".")
	run(t, other, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "late")
	run(t, other, "push", "-q", Remote, "fleet/moved")

	done, err := PruneRemote(repo, bs)
	if err == nil {
		t.Error("PruneRemote: want an error for the refused lease")
	}
	if strings.Join(done, " ") != "fleet/done" {
		t.Errorf("deleted %v, want [fleet/done]", done)
	}
	if h := remoteHeads(t, repo); h != "fleet/live fleet/moved fleet/open main" {
		t.Errorf("remote heads = %q", h)
	}
}

func TestDeleteMergedRemote(t *testing.T) {
	repo, _ := remoteRepo(t)
	pushBranch(t, repo, "fleet/a", "a1.go")
	pushBranch(t, repo, "fleet/b", "b1.go")
	run(t, repo, "branch", "fleet/local", "main") // never pushed
	rebaseMerge(t, repo, "fleet/a")

	for _, c := range []struct {
		branch string
		want   bool
	}{{"fleet/b", false}, {"fleet/local", false}, {"main", false}, {"fleet/a", true}} {
		got, err := DeleteMergedRemote(repo, c.branch)
		if err != nil || got != c.want {
			t.Errorf("DeleteMergedRemote(%s) = %v, %v; want %v", c.branch, got, err, c.want)
		}
	}
	if h := remoteHeads(t, repo); h != "fleet/b main" {
		t.Errorf("remote heads = %q", h)
	}
	// Already gone on the remote (GitHub auto-delete): no error, and the
	// stale tracking ref is dropped.
	run(t, repo, "update-ref", "refs/remotes/"+Remote+"/fleet/a", "main")
	if got, err := DeleteMergedRemote(repo, "fleet/a"); got || err != nil {
		t.Errorf("gone branch: %v, %v", got, err)
	}
	if _, err := gitCtxT(repo, "rev-parse", "--verify", "--quiet", "refs/remotes/"+Remote+"/fleet/a"); err == nil {
		t.Error("stale tracking ref kept")
	}
}
