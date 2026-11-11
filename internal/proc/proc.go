// Package proc finds and stops a process tree through /proc, so stopping
// an agent also stops what its CLI started (DESIGN §7, ADR-44, #26).
package proc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// root is /proc; a var so tests could point it elsewhere.
var root = "/proc"

// stat reads the parent pid, state and start time of pid.
func stat(pid int) (ppid int, state byte, start uint64, ok bool) {
	b, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, 0, 0, false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')') // comm may hold spaces and parens
	if i < 0 {
		return 0, 0, 0, false
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return 0, 0, 0, false
	}
	ppid, _ = strconv.Atoi(f[1])
	start, _ = strconv.ParseUint(f[19], 10, 64)
	return ppid, f[0][0], start, true
}

// Tree returns pid and all its descendants, pid first. It is empty when
// pid is not running or /proc can't be read.
func Tree(pid int) []Proc {
	_, _, st, ok := stat(pid)
	if pid <= 0 || !ok {
		return nil
	}
	ents, _ := os.ReadDir(root)
	kids := map[int][]Proc{}
	for _, e := range ents {
		p, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if pp, _, s, ok := stat(p); ok {
			kids[pp] = append(kids[pp], Proc{p, s})
		}
	}
	out := []Proc{{pid, st}}
	for i := 0; i < len(out); i++ {
		out = append(out, kids[out[i].PID]...)
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
		sysx.Kill(p.PID, syscall.SIGTERM)
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
		sysx.Kill(p.PID, syscall.SIGKILL)
	}
	// SIGKILL is delivered asynchronously; wait briefly so callers see the tree gone.
	for end := time.Now().Add(500 * time.Millisecond); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		gone := true
		for _, p := range live {
			if alive(p) {
				gone = false
				break
			}
		}
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
