package main

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/watch"
)

// uiReexec makes the overview follow a rebuilt bin/fleet as the watcher
// and `fleet mcp` do (ADR-62): a new binary that passes the probe quits the
// TUI, which gives the terminal back, and cmdUI then execs the new binary
// in the same pane with the same arguments.
type uiReexec struct {
	bin     *watch.Binary
	want    atomic.Bool
	mu      sync.Mutex
	stopped bool // the TUI has ended; a probe that finishes now must not exec
}

var errUIStopped = errors.New("overview already stopped")

func newUIReexec(exe, self string, probe func(string) error, quit func()) *uiReexec {
	r := &uiReexec{}
	r.bin = &watch.Binary{Exe: exe, Self: self, Component: "ui", Reexec: func() error {
		if err := probe(exe); err != nil {
			return err
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.stopped {
			return errUIStopped
		}
		r.want.Store(true)
		quit()
		return nil
	}}
	r.bin.Init()
	return r
}

// check looks at the binary once, until a hand-over is arranged.
func (r *uiReexec) check() {
	if !r.want.Load() {
		r.bin.Changed()
	}
}

func (r *uiReexec) wanted() bool { return r.want.Load() }

// halt marks the TUI as ended and reports whether it ended for a hand-over:
// a user's quit while a probe ran stays a quit.
func (r *uiReexec) halt() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	return r.want.Load()
}

// run checks every interval until ctx ends or a hand-over is arranged.
func (r *uiReexec) run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for !r.wanted() {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.check()
		}
	}
}

// exec replaces the overview with the binary at exe (same pid, arguments
// and environment).
func (r *uiReexec) exec(exe string) error {
	logx.For("ui").Info("re-exec", "exe", exe, "pid", os.Getpid())
	return syscall.Exec(exe, os.Args, os.Environ())
}
