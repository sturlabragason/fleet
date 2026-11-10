package supervisor

import (
	"os"
	"path/filepath"
	"syscall"
)

// Lock takes an exclusive lock on admiral/reset.lock so concurrent resets
// (M-R, overview R, fleet_handoff) run one at a time. Call unlock when done.
func Lock(home string) (unlock func(), err error) { return LockOf(home, Main) }

// LockOf is Lock for one admiral (admirals/<name>/reset.lock).
func LockOf(home, name string) (unlock func(), err error) { return lockOf(home, name, 0) }

// TryLockOf is LockOf that fails at once when another process holds it.
func TryLockOf(home, name string) (unlock func(), err error) {
	return lockOf(home, name, syscall.LOCK_NB)
}

func lockOf(home, name string, nb int) (unlock func(), err error) {
	dir, err := mkdirOf(home, name)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "reset.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|nb); err != nil {
		f.Close()
		return nil, err
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
