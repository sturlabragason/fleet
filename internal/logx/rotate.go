package logx

import (
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/sturlabragason/fleet/internal/sysx"
)

// fleet.log is shared by every fleet process (the watcher, the overview,
// each agent's `fleet mcp`), all appending. It is rotated by size (ADR-113):
// at MaxSize the file becomes fleet.log.1 (and .1 becomes .2, up to Keep),
// and every process, at its next check, sees the path now names another
// file and reopens it.
var (
	MaxSize    int64 = 5 << 20
	Keep             = 2
	checkEvery       = 5 * time.Second
)

type rotator struct {
	mu      sync.Mutex
	path    string
	f       *os.File
	checked time.Time
}

func openRotator(path string) (*rotator, error) {
	r := &rotator{path: path}
	if err := r.reopen(); err != nil {
		return nil, err
	}
	r.check(time.Now())
	return r, nil
}

func (r *rotator) reopen() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if r.f != nil {
		r.f.Close()
	}
	r.f = f
	return nil
}

func (r *rotator) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now := time.Now(); now.Sub(r.checked) >= checkEvery {
		r.check(now)
	}
	return r.f.Write(p)
}

// check rotates the file when it is past MaxSize, and reopens it when
// another process rotated it.
func (r *rotator) check(now time.Time) {
	r.checked = now
	cur, err := r.f.Stat()
	if err != nil {
		return
	}
	on, err := os.Stat(r.path)
	if err != nil || !os.SameFile(cur, on) {
		_ = r.reopen()
		return
	}
	if on.Size() < MaxSize {
		return
	}
	lf, err := os.OpenFile(r.path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return
	}
	defer lf.Close()
	if sysx.Flock(lf, sysx.LockEx) != nil {
		return
	}
	defer sysx.Flock(lf, sysx.LockUn)
	// Another process may have rotated while this one waited for the lock.
	if on, err := os.Stat(r.path); err == nil && os.SameFile(cur, on) && on.Size() >= MaxSize {
		for i := Keep; i > 1; i-- {
			_ = os.Rename(r.path+"."+strconv.Itoa(i-1), r.path+"."+strconv.Itoa(i))
		}
		_ = os.Rename(r.path, r.path+".1")
	}
	_ = r.reopen()
}
