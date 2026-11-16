// Package proc finds and stops a process tree through /proc (ps(1) on
// darwin, which has no /proc), so stopping an agent also stops what its CLI
// started (DESIGN §7, ADR-44, #26).
package proc

import (
	"os"
	"slices"
	"syscall"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/sysx"
)

// Proc is one process, identified by pid and start time so a reused pid is
// never mistaken for it.
type Proc struct {
	PID   int
	Start uint64
}

// Tree returns pid and all its descendants, pid first, leaving out a GUI
// descendant and everything below it: a desktop app the agent opened for
// the user outlives the agent. It is empty when pid is not running or
// the process table can't be read.
func Tree(pid int) []Proc {
	_, _, st, ok := stat(pid)
	if pid <= 0 || !ok {
		return nil
	}
	kids := children()
	out := []Proc{{pid, st}}
	for i := 0; i < len(out); i++ {
		for _, k := range kids[out[i].PID] {
			if !GUI(k.PID) {
				out = append(out, k)
			}
		}
	}
	return out
}

// alive reports whether p is still the same running process: a zombie or
// a pid now held by another process counts as gone.
func alive(p Proc) bool {
	_, state, s, ok := stat(p.PID)
	return ok && s == p.Start && state != 'Z' && state != 'X'
}

// Kill sends SIGTERM to every process in ps that is still running, waits up
// to grace for them to exit, then SIGKILLs the rest. It never signals pid
// 1 or below, or fleet's own process.
func Kill(ps []Proc, grace time.Duration) {
	log := logx.For("proc")
	self := os.Getpid()
	var live []Proc
	for _, p := range ps {
		if p.PID > 1 && p.PID != self && alive(p) {
			live = append(live, p)
		}
	}
	if len(live) == 0 {
		return
	}
	for _, p := range live {
		if err := sysx.Kill(p.PID, syscall.SIGTERM); err != nil {
			log.Debug("SIGTERM failed; it may be gone already", "pid", p.PID, "err", err)
		}
	}
	deadline := time.Now().Add(grace)
	for {
		rest := live[:0]
		for _, p := range live {
			if alive(p) {
				rest = append(rest, p)
			}
		}
		live = rest
		if len(live) == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, p := range live {
		log.Warn("SIGKILL after grace", "pid", p.PID)
		if err := sysx.Kill(p.PID, syscall.SIGKILL); err != nil {
			log.Debug("SIGKILL failed; it may be gone already", "pid", p.PID, "err", err)
		}
	}
	// SIGKILL is delivered asynchronously; wait briefly so callers see the tree gone.
	for end := time.Now().Add(500 * time.Millisecond); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		gone := !slices.ContainsFunc(live, alive)
		if gone {
			break
		}
	}
	log.Debug("stopped process tree", "procs", len(ps), "killed", len(live))
}

// Running reports whether pid is a live (not zombie) process.
func Running(pid int) bool {
	_, state, _, ok := stat(pid)
	return ok && state != 'Z' && state != 'X'
}
