package sessions

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/sturlabragason/fleet/internal/proc"
)

// darwin has no /proc: command lines come from ps (proc.Cmdlines), split on
// spaces. A session id never holds one, so holds still finds it.

// argv is pid's command line.
func argv(pid int) []string {
	for _, c := range proc.Cmdlines() {
		if c.PID == pid {
			return c.Argv
		}
	}
	return nil
}

// commOf is the comm a process with argv would have: its program's base
// name, without a login shell's "-".
func commOf(argv []string) string { return strings.TrimPrefix(filepath.Base(argv[0]), "-") }

// ProcPPid is pid's parent pid (0 when unknown).
func ProcPPid(pid int) int { return proc.Parent(pid) }

func scanProcs() []Proc {
	skip := Ancestors(os.Getpid(), ProcPPid)
	var out []Proc
	for _, c := range proc.Cmdlines() {
		if skip[c.PID] || len(c.Argv) == 0 {
			continue
		}
		out = append(out, Proc{PID: c.PID, Comm: commOf(c.Argv), Argv: c.Argv})
	}
	return out
}
