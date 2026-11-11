//go:build !windows

package sysx

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// Flock applies or removes an advisory lock on f (flock(2)).
func Flock(f *os.File, how int) error {
	var op int
	if how&LockSh != 0 {
		op |= syscall.LOCK_SH
	}
	if how&LockEx != 0 {
		op |= syscall.LOCK_EX
	}
	if how&LockNB != 0 {
		op |= syscall.LOCK_NB
	}
	if how&LockUn != 0 {
		op |= syscall.LOCK_UN
	}
	return syscall.Flock(int(f.Fd()), op)
}

// Kill sends sig to pid; a negative pid is a process group.
func Kill(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }

// Detach starts cmd in a new session, so it outlives this process's terminal.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// KillGroupOnCancel puts cmd in its own process group and makes the context
// cancel SIGKILL the whole group, so children don't outlive it.
func KillGroupOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}

// ShellCommand runs line with sh -c.
func ShellCommand(ctx context.Context, line string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", line)
}

// Exec replaces this process with argv0 (execve(2)).
func Exec(argv0 string, argv, env []string) error { return syscall.Exec(argv0, argv, env) }

// NotifyResize relays terminal resizes (SIGWINCH) to ch.
func NotifyResize(ch chan<- os.Signal) { signal.Notify(ch, syscall.SIGWINCH) }

// KillTree ends pid and its process group (Detach made it a group leader).
func KillTree(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		return syscall.Kill(pid, syscall.SIGTERM)
	}
	return nil
}
