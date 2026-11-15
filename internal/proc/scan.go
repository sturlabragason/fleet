package proc

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Info is one process as the resource guard sees it (ADR-122).
type Info struct {
	Proc
	PPID  int
	Comm  string
	Ticks uint64        // user + system CPU time, in clock ticks
	RSS   uint64        // resident memory, bytes
	Age   time.Duration // time since it started
	Owner string        // the owner tag from its environment, "" when untagged
	// Daemon: it leads its own session and has no terminal, like a
	// browser, an ssh master or a tmux server some command started.
	Daemon bool
}

// clockTick is USER_HZ, 100 on every Linux build fleet runs on.
const clockTick = 100

// TicksPerSecond converts Ticks to CPU seconds.
const TicksPerSecond = clockTick

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

// Shells are the commands whose children are an agent's work (a build, a
// test run, a server) rather than the agent itself.
var Shells = map[string]bool{"sh": true, "bash": true, "dash": true, "zsh": true, "fish": true}

// Own returns root's process and its descendants, leaving out everything
// below a shell other than root itself: the agent's own processes (the
// tool, its helpers, fleet's host and MCP server), not the work it runs.
func Own(ps []Info, root int) []Info {
	kids := map[int][]int{}
	by := make(map[int]Info, len(ps))
	for _, p := range ps {
		kids[p.PPID] = append(kids[p.PPID], p.PID)
		by[p.PID] = p
	}
	r, ok := by[root]
	if !ok {
		return nil
	}
	out := []Info{r}
	for i := 0; i < len(out); i++ {
		if i > 0 && Shells[out[i].Comm] {
			continue
		}
		for _, k := range kids[out[i].PID] {
			out = append(out, by[k])
		}
	}
	return out
}
