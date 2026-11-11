package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/sysx"
)

// lockDir holds one lock file per trust store, under fleet's own home so
// nothing is left behind in a directory the tool itself scans. Two fleet
// processes (a spawn and the overview's queue drain) can mark the same file
// at once; the lock makes read-modify-write one step for them. The tool
// itself does not take it, which is why the merge happens immediately
// before the write and the replace is atomic.
const lockDir = "locks"

// lockPath names the lock for a store by a hash of its path, so two stores
// with the same base name cannot share one.
func lockPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(logx.Home(), lockDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(dir, hex.EncodeToString(sum[:8])+".lock"), nil
}

// editor rewrites the current contents of a trust store. changed=false
// leaves the file untouched.
type editor func(old []byte) (data []byte, changed bool, err error)

// edit applies fn to path under a lock and replaces the file atomically:
// the bytes fn sees are the bytes on disk at that moment, so a concurrent
// write by the tool is merged rather than clobbered. A missing file is fn's
// empty input and is created; a missing parent directory is an error,
// because a tool that has never run has nothing for fleet to pre-answer.
func edit(path string, fn editor) (bool, error) {
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		return false, err
	}
	unlock, err := lock(path)
	if err != nil {
		return false, err
	}
	defer unlock()

	old, err := os.ReadFile(path)
	mode := os.FileMode(0o600)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, err
		}
		old = nil
	} else if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	data, changed, err := fn(old)
	if err != nil || !changed {
		return false, err
	}
	return true, replace(path, data, mode)
}

// lock takes an exclusive flock on the store's lock file and returns its
// release. The lock file is separate so the store itself is only ever
// replaced by rename, never truncated in place.
func lock(path string) (func(), error) {
	name, err := lockPath(path)
	if err != nil {
		return nil, err
	}
	fh, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := sysx.Flock(fh, sysx.LockEx); err != nil {
		fh.Close()
		return nil, fmt.Errorf("lock %s: %w", fh.Name(), err)
	}
	return func() {
		sysx.Flock(fh, sysx.LockUn)
		fh.Close()
	}, nil
}

// replace writes data to a temp file in the same directory and renames it
// over path, so a reader sees either the old file or the new one.
func replace(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".fleet-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once the rename succeeded
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
