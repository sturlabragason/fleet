//go:build !darwin

package sessions

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// argv reads /proc/<pid>/cmdline split on NUL.
func argv(pid int) []string {
	b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	s := strings.TrimRight(string(b), "\x00")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\x00")
}

func comm(pid int) string {
	b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	return strings.TrimSpace(string(b))
}

// ProcPPid reads PPid from /proc/<pid>/status (0 when unknown).
func ProcPPid(pid int) int {
	b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	for l := range strings.SplitSeq(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "PPid:"); ok {
			n, _ := strconv.Atoi(strings.TrimSpace(v))
			return n
		}
	}
	return 0
}

func scanProcs() []Proc {
	ents, _ := os.ReadDir("/proc")
	skip := Ancestors(os.Getpid(), ProcPPid)
	var out []Proc
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || skip[pid] {
			continue
		}
		if a := argv(pid); len(a) > 0 {
			out = append(out, Proc{PID: pid, Comm: comm(pid), Argv: a})
		}
	}
	return out
}
