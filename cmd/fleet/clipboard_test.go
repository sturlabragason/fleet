package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runCopy feeds sel to the guarded copy-command the way tmux's copy-pipe does.
func runCopy(t *testing.T, cc, sel string, env ...string) {
	t.Helper()
	c := exec.Command("sh", "-c", cc)
	c.Stdin = strings.NewReader(sel)
	c.Env = append(os.Environ(), env...)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("copy-command: %v: %s", err, out)
	}
}

// A click into a pane runs copy-command with an empty or blank selection;
// passing that on wiped the user's clipboard.
func TestGuardedCopySkipsBlankSelections(t *testing.T) {
	out := filepath.Join(t.TempDir(), "clip")
	cc := guardedCopyCommand("tee " + out)
	for _, c := range []struct{ name, sel, want string }{
		{"empty", "", ""},
		{"whitespace", " \n\t \n", ""},
		{"normal", "  hello it's\n\n", "  hello it's\n\n"},
	} {
		os.Remove(out)
		runCopy(t, cc, c.sel)
		got, err := os.ReadFile(out)
		if c.want == "" {
			if err == nil {
				t.Errorf("%s: copy-command ran with %q", c.name, got)
			}
			continue
		}
		if string(got) != c.want {
			t.Errorf("%s: clipboard got %q, want %q", c.name, got, c.want)
		}
	}
}

// Each wl-copy fleet starts replaces the previous one, so one that never got
// the selection (GNOME without data-control) does not linger.
func TestGuardedWlCopyReplacesThePreviousOne(t *testing.T) {
	bin, run := t.TempDir(), t.TempDir()
	fake := "#!/bin/sh\ntrap 'kill $!; exit 0' TERM\ncat >\"$0.out\"\nsleep 30 & wait\n"
	if err := os.WriteFile(filepath.Join(bin, "wl-copy"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + bin + ":" + os.Getenv("PATH"), "XDG_RUNTIME_DIR=" + run}
	cc := guardedCopyCommand("wl-copy")
	pidOf := func() int {
		var p int
		for i := 0; i < 100; i++ {
			b, _ := os.ReadFile(filepath.Join(run, "fleet-wl-copy.pid"))
			if p, _ = strconv.Atoi(strings.TrimSpace(string(b))); p > 0 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		return p
	}
	alive := func(p int) bool { return syscall.Kill(p, 0) == nil }
	runCopy(t, cc, "one", env...)
	first := pidOf()
	if first <= 0 {
		t.Fatal("no pidfile after the first copy")
	}
	t.Cleanup(func() { syscall.Kill(first, syscall.SIGTERM) })
	runCopy(t, cc, "two", env...)
	second := pidOf()
	if second <= 0 {
		t.Fatal("no pidfile after the second copy")
	}
	t.Cleanup(func() { syscall.Kill(second, syscall.SIGTERM) })
	if second == first {
		t.Fatalf("pidfile still names the first wl-copy %d", first)
	}
	gone := false
	for i := 0; i < 100 && !gone; i++ {
		var ws syscall.WaitStatus
		syscall.Wait4(first, &ws, syscall.WNOHANG, nil)
		gone = !alive(first)
		time.Sleep(20 * time.Millisecond)
	}
	if !gone {
		syscall.Kill(first, syscall.SIGTERM)
		t.Errorf("the first wl-copy %d still runs after the second copy", first)
	}
	runCopy(t, cc, "  ", env...)
	if p := pidOf(); p != second || !alive(second) {
		t.Errorf("a blank copy replaced the wl-copy holding the clipboard (pid %d, was %d)", p, second)
	}
}
