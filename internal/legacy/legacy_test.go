package legacy

import (
	"os"
	"path/filepath"
	"testing"
)

func paths(t *testing.T) (dir, nw, old string) {
	dir = t.TempDir()
	return dir, filepath.Join(dir, "admiral"), filepath.Join(dir, "supervisor")
}

func isLink(t *testing.T, p string) {
	t.Helper()
	st, err := os.Lstat(p)
	if err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink: %v %v", p, st, err)
	}
	if dst, _ := os.Readlink(p); dst != "admiral" {
		t.Fatalf("link target %q", dst)
	}
}

func TestMoveDirMovesOldAndLinks(t *testing.T) {
	_, nw, old := paths(t)
	os.MkdirAll(filepath.Join(old, "handoffs"), 0o755)
	os.WriteFile(filepath.Join(old, "session.json"), []byte("x"), 0o644)
	if got := MoveDir(nw, old); got != nw {
		t.Fatalf("got %s", got)
	}
	if b, err := os.ReadFile(filepath.Join(nw, "session.json")); err != nil || string(b) != "x" {
		t.Fatalf("contents: %q %v", b, err)
	}
	if st, err := os.Lstat(nw); err != nil || !st.IsDir() {
		t.Fatalf("new is not a real dir: %v", err)
	}
	isLink(t, old)
	// writes through the old path land in the new dir
	os.WriteFile(filepath.Join(old, "handoff.md"), []byte("h"), 0o644)
	if _, err := os.Stat(filepath.Join(nw, "handoff.md")); err != nil {
		t.Fatal(err)
	}
	if got := MoveDir(nw, old); got != nw { // second call: no-op
		t.Fatalf("again: %s", got)
	}
	isLink(t, old)
}

func TestMoveDirBothRealUsesNew(t *testing.T) {
	_, nw, old := paths(t)
	os.Mkdir(nw, 0o755)
	os.Mkdir(old, 0o755)
	if got := MoveDir(nw, old); got != nw {
		t.Fatalf("got %s", got)
	}
	if st, _ := os.Lstat(old); !st.IsDir() {
		t.Fatal("old real dir was touched")
	}
}

func TestMoveDirNeitherCreatesNothing(t *testing.T) {
	dir, nw, old := paths(t)
	if got := MoveDir(nw, old); got != nw {
		t.Fatalf("got %s", got)
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Fatalf("created %v", es)
	}
}

func TestMkdirAllLinksFreshDir(t *testing.T) {
	_, nw, old := paths(t)
	got, err := MkdirAll(nw, old)
	if err != nil || got != nw {
		t.Fatalf("%s %v", got, err)
	}
	if st, err := os.Lstat(nw); err != nil || !st.IsDir() {
		t.Fatal("new dir missing")
	}
	isLink(t, old)
}

func TestMoveDirRenameFailsUsesOld(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir, nw, old := paths(t)
	os.Mkdir(old, 0o755)
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if got := MoveDir(nw, old); got != old {
		t.Fatalf("got %s", got)
	}
	if st, _ := os.Lstat(old); !st.IsDir() {
		t.Fatal("old dir lost")
	}
}

// An old binary recreated the old dir between rename and symlink.
func TestLinkReplacesRecreatedEmptyDir(t *testing.T) {
	_, nw, old := paths(t)
	os.Mkdir(nw, 0o755)
	os.Mkdir(old, 0o755)
	link(nw, old)
	isLink(t, old)
}

func TestLinkKeepsRecreatedDirWithFiles(t *testing.T) {
	_, nw, old := paths(t)
	os.Mkdir(nw, 0o755)
	os.Mkdir(old, 0o755)
	os.WriteFile(filepath.Join(old, "inbox.jsonl"), []byte("r\n"), 0o644)
	link(nw, old)
	if b, err := os.ReadFile(filepath.Join(old, "inbox.jsonl")); err != nil || string(b) != "r\n" {
		t.Fatalf("old dir contents lost: %q %v", b, err)
	}
}

func TestResolveHasNoSideEffects(t *testing.T) {
	_, nw, old := paths(t)
	if got := Resolve(nw, old); got != nw {
		t.Fatalf("neither: %s", got)
	}
	os.Mkdir(old, 0o755)
	if got := Resolve(nw, old); got != old {
		t.Fatalf("old only: %s", got)
	}
	if st, err := os.Lstat(old); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		t.Fatal("Resolve moved the old dir")
	}
	if _, err := os.Lstat(nw); err == nil {
		t.Fatal("Resolve created the new dir")
	}
	os.Mkdir(nw, 0o755)
	if got := Resolve(nw, old); got != nw {
		t.Fatalf("both: %s", got)
	}
}
