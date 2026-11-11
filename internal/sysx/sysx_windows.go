package sysx

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

var (
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	lockFileEx   = kernel32.NewProc("LockFileEx")
	unlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockfileFailImmediately = 0x1
	lockfileExclusiveLock   = 0x2
	detachedProcess         = 0x8
	stillActive             = 259
)

// Flock applies or removes a lock on f with LockFileEx. Unlike flock(2) the
// lock is mandatory for the locked range, so fleet locks only its own
// lock files, never the data it guards.
func Flock(f *os.File, how int) error {
	h := uintptr(f.Fd())
	ol := new(syscall.Overlapped)
	if how&LockUn != 0 {
		r, _, err := unlockFileEx.Call(h, 0, 1, 0, uintptr(unsafe.Pointer(ol)))
		if r == 0 {
			return err
		}
		return nil
	}
	var flags uintptr
	if how&LockEx != 0 {
		flags |= lockfileExclusiveLock
	}
	if how&LockNB != 0 {
		flags |= lockfileFailImmediately
	}
	r, _, err := lockFileEx.Call(h, flags, 0, 1, 0, uintptr(unsafe.Pointer(ol)))
	if r == 0 {
		return err
	}
	return nil
}

// Kill ends pid; Windows has no signals to send another process, so any
// signal but 0 terminates it. Signal 0 checks that pid is alive. A negative
// pid (a Unix process group) ends that process.
func Kill(pid int, sig syscall.Signal) error {
	if pid < 0 {
		pid = -pid
	}
	if sig == 0 {
		h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
		if err != nil {
			return syscall.ESRCH
		}
		defer syscall.CloseHandle(h)
		var code uint32
		if err := syscall.GetExitCodeProcess(h, &code); err != nil {
			return err
		}
		if code != stillActive {
			return syscall.ESRCH
		}
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// Detach starts cmd without a console and in its own process group.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess}
}

// KillGroupOnCancel puts cmd in its own process group; the default cancel
// (TerminateProcess) ends it. Its children are not ended with it.
func KillGroupOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// Exec runs argv0 in place of this process as far as Windows allows: the
// child inherits the console and this process exits with its exit code.
func Exec(argv0 string, argv, env []string) error {
	cmd := exec.Command(argv0)
	cmd.Args = argv
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		os.Exit(ee.ExitCode())
	}
	if err != nil {
		return err
	}
	os.Exit(0)
	return nil
}

// NotifyResize does nothing: Windows consoles send no resize signal. Callers
// that poll the size still see it change.
func NotifyResize(chan<- os.Signal) {}
