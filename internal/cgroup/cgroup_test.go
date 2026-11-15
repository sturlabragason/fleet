package cgroup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeRoots points procRoot and fsRoot at a temp dir and gives pid 42 the
// cgroup line cg.
func fakeRoots(t *testing.T, cg string) string {
	t.Helper()
	d := t.TempDir()
	old := [2]string{procRoot, fsRoot}
	procRoot, fsRoot = filepath.Join(d, "proc"), filepath.Join(d, "cgroup")
	t.Cleanup(func() { procRoot, fsRoot = old[0], old[1] })
	write(t, filepath.Join(procRoot, "42", "cgroup"), cg)
	return d
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOf(t *testing.T) {
	for _, tc := range []struct {
		name, cg, want string
		err            error
	}{
		{"scope", "1:net_cls:/\n0::/user.slice/a.slice/tmux-spawn-1.scope\n", "user.slice/a.slice/tmux-spawn-1.scope", nil},
		{"root", "0::/\n", "", ErrNoScope},
		{"v1 only", "4:memory:/foo\n", "", ErrNoScope},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeRoots(t, tc.cg)
			got, err := Of(42)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if tc.want != "" && got != filepath.Join(fsRoot, tc.want) {
				t.Errorf("Of = %q, want %q under %q", got, tc.want, fsRoot)
			}
		})
	}
	fakeRoots(t, "")
	if _, err := Of(7); err == nil {
		t.Error("Of of a missing pid: no error")
	}
}

func TestReclaimCache(t *testing.T) {
	for _, tc := range []struct {
		name, stat string
		reclaim    bool // memory.reclaim exists
		want       string
		n          uint64
		wantErr    bool
	}{
		{"file pages", "anon 100\nfile 4096\nkernel 1\n", true, "4096 swappiness=0", 4096, false},
		{"no cache", "anon 100\nfile 0\n", true, "", 0, false},
		{"no reclaim file", "file 4096\n", false, "", 4096, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "memory.stat"), tc.stat)
			if tc.reclaim {
				write(t, filepath.Join(dir, "memory.reclaim"), "")
			}
			n, err := ReclaimCache(t.Context(), dir)
			if (err != nil) != tc.wantErr || n != tc.n {
				t.Fatalf("ReclaimCache = %d, %v; want %d, err %v", n, err, tc.n, tc.wantErr)
			}
			if tc.reclaim {
				b, _ := os.ReadFile(filepath.Join(dir, "memory.reclaim"))
				if string(b) != tc.want {
					t.Errorf("memory.reclaim = %q, want %q", b, tc.want)
				}
			}
		})
	}
	if _, err := ReclaimCache(t.Context(), t.TempDir()); err == nil {
		t.Error("no memory.stat: no error")
	}
}
