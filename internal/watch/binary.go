package watch

import (
	"os"

	"github.com/sturlabragason/fleet/internal/logx"
)

// Binary follows the fleet binary on disk for a resident process: the
// watcher and the overview (ADR-62). Changed is called now and then; when
// the file at Exe is no longer the one this process started from, it calls
// Reexec. A failed Reexec is not retried until the file changes again.
type Binary struct {
	// Exe is the fleet binary on disk.
	Exe string
	// Self is where the running binary is stat'ed ("" = /proc/self/exe).
	Self string
	// Reexec takes the process onto the new binary. It returns on failure
	// (the old code keeps running), or after it has arranged the hand-over.
	Reexec func() error
	// Component names the process in the log.
	Component string

	info, fail os.FileInfo
}

// Init records the running binary's identity: the inode it was started
// from, not whatever sits at Exe now. Changed calls it too.
func (b *Binary) Init() {
	if b.info != nil {
		return
	}
	self := b.Self
	if self == "" {
		self = "/proc/self/exe"
	}
	if fi, err := os.Stat(self); err == nil {
		b.info = fi
	}
}

// Changed checks the binary once and calls Reexec when it was replaced. It
// reports true only when the binary changed and there is no Reexec, so the
// caller should stop.
func (b *Binary) Changed() bool {
	if b.Init(); b.info == nil {
		return false
	}
	fi, err := os.Stat(b.Exe)
	if err != nil || sameFile(fi, b.info) || sameFile(fi, b.fail) {
		return false
	}
	log := logx.For(b.Component)
	if b.Reexec == nil {
		log.Info("stop", "reason", "binary changed", "exe", b.Exe)
		return true
	}
	log.Info("re-exec", "exe", b.Exe, "new_mtime", fi.ModTime(), "pid", os.Getpid())
	if err := b.Reexec(); err != nil {
		log.Warn("re-exec failed; keeping the old code", "exe", b.Exe, "err", err)
		b.fail = fi
		return false
	}
	b.info = fi
	return false
}
