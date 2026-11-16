//go:build !darwin

package proc

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var pageSize = uint64(os.Getpagesize())

// Scan lists every process it can read, with ownerEnv's value from the
// environment of the ones this user owns. It is empty without /proc.
func Scan(ownerEnv string) []Info {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	up := uptime()
	var out []Info
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		in, ok := info(pid, up)
		if !ok {
			continue
		}
		in.Owner = envValue(pid, ownerEnv)
		out = append(out, in)
	}
	return out
}

func info(pid int, up time.Duration) (Info, bool) {
	b, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "stat"))
	if err != nil {
		return Info{}, false
	}
	s := string(b)
	i, j := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if i < 0 || j < i {
		return Info{}, false
	}
	f := strings.Fields(s[j+1:])
	if len(f) < 22 || f[0] == "Z" || f[0] == "X" {
		return Info{}, false
	}
	n := func(k int) uint64 { v, _ := strconv.ParseUint(f[k], 10, 64); return v }
	ppid, _ := strconv.Atoi(f[1])
	start := n(19)
	sid, _ := strconv.Atoi(f[3])
	in := Info{Proc: Proc{pid, start}, PPID: ppid, Comm: s[i+1 : j], Ticks: n(11) + n(12), RSS: n(21) * pageSize,
		Daemon: sid == pid && n(4) == 0}
	if up > 0 {
		in.Age = up - time.Duration(start)*time.Second/clockTick
	}
	return in, true
}

func uptime() time.Duration {
	b, err := os.ReadFile(filepath.Join(root, "uptime"))
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0
	}
	return time.Duration(v * float64(time.Second))
}

// envValue reads one variable from pid's environment; "" when unset or
// unreadable (another user's process).
func envValue(pid int, key string) string {
	if key == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "environ"))
	if err != nil {
		return ""
	}
	pre := []byte(key + "=")
	for kv := range bytes.SplitSeq(b, []byte{0}) {
		if v, ok := bytes.CutPrefix(kv, pre); ok {
			return string(v)
		}
	}
	return ""
}

// Swap is how much of pid is swapped out, in bytes (VmSwap); 0 when unknown.
// A process the kernel pushed into swap has a small RSS but still holds the
// memory, so the guard counts both.
func Swap(pid int) uint64 {
	b, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "status"))
	if err != nil {
		return 0
	}
	for l := range strings.SplitSeq(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "VmSwap:"); ok {
			f := strings.Fields(v)
			if len(f) > 0 {
				kb, _ := strconv.ParseUint(f[0], 10, 64)
				return kb * 1024
			}
		}
	}
	return 0
}
