package testenv

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
)

const (
	prSetNoNewPrivs = 38
	prGetNoNewPrivs = 39
)

// noNewPrivs sets no_new_privs on the test binary so every tmux server it
// starts inherits it. tmux records each pane in utmp/wtmp through the setgid
// utempter helper; with no_new_privs the kernel ignores the setgid bit, the
// helper cannot open wtmp, and no record is written. A run used to add
// hundreds, and accounts-daemon re-reads wtmp on every write.
//
// The flag is per thread. AllThreadsSyscall sets it on all of them, but not
// when cgo is linked in; then it is set on this thread and the binary
// re-executes itself, so the new process starts with it on its one thread.
func noNewPrivs() {
	if hasNoNewPrivs() {
		return
	}
	if _, _, e := syscall.AllThreadsSyscall(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0); e == 0 {
		return
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0); e != 0 {
		fmt.Fprintln(os.Stderr, "testenv: no_new_privs:", e)
		return
	}
	exe, err := os.Executable()
	if err == nil {
		err = syscall.Exec(exe, os.Args, os.Environ())
	}
	fmt.Fprintln(os.Stderr, "testenv: re-exec with no_new_privs:", err)
}

func hasNoNewPrivs() bool {
	r, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, prGetNoNewPrivs, 0, 0)
	return e == 0 && r == 1
}
