//go:build !darwin

package proc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// OwnerTags reports whether Scan can read Info.Owner from a process's
// environment.
const OwnerTags = true

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

// children maps every readable process's parent pid to it.
func children() map[int][]Proc {
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
	return kids
}

// SelfExe is the path to stat for the running binary: the inode it was
// started from, whatever sits at its path now.
func SelfExe() string { return filepath.Join(root, "self", "exe") }

// RunsExe reports whether pid runs the binary at exe as it is now (same
// file, same modtime and size); ok is false when either can't be read.
func RunsExe(pid int, exe string) (runs, ok bool) {
	running, err := os.Stat(filepath.Join(root, strconv.Itoa(pid), "exe"))
	if err != nil {
		return false, false
	}
	want, err := os.Stat(exe)
	if err != nil {
		return false, false
	}
	return os.SameFile(running, want) && running.ModTime().Equal(want.ModTime()) && running.Size() == want.Size(), true
}
