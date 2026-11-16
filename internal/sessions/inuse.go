package sessions

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/sturlabragason/fleet/internal/sysx"
)

// Proc is one running process: its comm name and argv.
type Proc struct {
	PID  int
	Comm string
	Argv []string
}

// Cmd is the argv joined with spaces, for messages.
func (p Proc) Cmd() string { return strings.Join(p.Argv, " ") }

// holds reports whether p is a tool process holding session id: id is a whole
// argv element or the value of a --flag=<id> element. Terminal multiplexers
// are skipped (a tmux server keeps the command it was started with).
func (p Proc) holds(id string) bool {
	if strings.HasPrefix(p.Comm, "tmux") || strings.HasPrefix(p.Comm, "screen") {
		return false
	}
	for _, a := range p.Argv {
		if a == id {
			return true
		}
		if f, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(f, "--") && v == id {
			return true
		}
	}
	return false
}

// Procs lists running processes other than this one and its parents (our own
// shell carries the id too). A var so tests can stub it.
var Procs = scanProcs

// Holder returns the live process holding session id (pid 0 when none): a
// Copilot inuse.<pid>.lock in one of the session paths, or a process in procs
// that holds it (see Proc.holds).
func Holder(id string, paths []string, procs []Proc) (int, string) {
	for _, m := range paths {
		locks, _ := filepath.Glob(filepath.Join(m, "inuse.*.lock"))
		for _, l := range locks {
			s := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(l), "inuse."), ".lock")
			pid, err := strconv.Atoi(s)
			if err != nil || pid <= 0 || !alive(pid) {
				continue
			}
			return pid, cmdline(pid)
		}
	}
	for _, p := range procs {
		if p.holds(id) {
			return p.PID, p.Cmd()
		}
	}
	return 0, ""
}

func alive(pid int) bool {
	err := sysx.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func cmdline(pid int) string { return strings.Join(argv(pid), " ") }

// Ancestors returns pid and all its parents (via ppid) up to, not including, 0.
func Ancestors(pid int, ppid func(int) int) map[int]bool {
	out := map[int]bool{}
	for pid > 0 && !out[pid] {
		out[pid] = true
		pid = ppid(pid)
	}
	return out
}
