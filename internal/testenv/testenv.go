// Package testenv keeps tests from leaking processes. A test binary whose
// TestMain calls Main tags its own environment with an owner mark; every tmux
// server, pane and `fleet watch` it starts inherits the mark. After the tests,
// any marked process still alive fails the run and is killed. Before them,
// marked processes whose run is gone (a timeout panic or a SIGKILL skips every
// t.Cleanup) are reaped. It reads /proc; elsewhere it only runs the tests.
package testenv

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sturlabragason/fleet/internal/sysx"
)

// Env is the variable that marks a process as started by a test run. Its
// value is "<pid>:<start time>" of the test binary, so a recycled pid is not
// taken for the run that set it.
const Env = "FLEET_TEST_OWNER"

// Proc is a process that carries the mark.
type Proc struct {
	PID   int
	Owner string
	Cmd   string
}

// Main runs the tests with every child marked and returns the exit code: 1
// if a marked process outlived them, which it then kills. On Linux the
// children run with no_new_privs, so test tmux panes write no wtmp records.
func Main(m interface{ Run() int }) int {
	noNewPrivs()
	Reap(Orphans())
	own := Owner(os.Getpid())
	os.Setenv(Env, own)
	code := m.Run()
	if left := WaitGone(own, 3*time.Second); len(left) > 0 {
		fmt.Fprintf(os.Stderr, "testenv: %d process(es) outlived the tests; stop them in t.Cleanup:\n", len(left))
		for _, p := range left {
			fmt.Fprintf(os.Stderr, "  pid %d: %s\n", p.PID, p.Cmd)
		}
		Reap(left)
		if code == 0 {
			code = 1
		}
	}
	return code
}

// Owner returns the mark for pid, or "" if it is not running.
func Owner(pid int) string {
	f := stat(pid)
	if f == nil {
		return ""
	}
	return strconv.Itoa(pid) + ":" + f[19]
}

// stat returns the fields of /proc/<pid>/stat after the command name, so
// f[0] is the state and f[19] the start time; nil if pid is gone.
func stat(pid int) []string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return nil
	}
	// The command name may hold spaces and parens; fields resume after the last ')'.
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return nil
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 20 {
		return nil
	}
	return f
}

func running(pid int) bool {
	f := stat(pid)
	return f != nil && f[0] != "Z" && f[0] != "X"
}

// Marked lists every other process that carries the mark.
func Marked() []Proc {
	ents, _ := os.ReadDir("/proc")
	self := os.Getpid()
	var out []Proc
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		owner := envOf(pid, Env)
		if owner == "" {
			continue
		}
		cmd, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		out = append(out, Proc{PID: pid, Owner: owner, Cmd: strings.TrimSpace(strings.ReplaceAll(string(cmd), "\x00", " "))})
	}
	return out
}

func envOf(pid int, key string) string {
	env, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return ""
	}
	for _, kv := range strings.Split(string(env), "\x00") {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}

// Of lists the marked processes owned by owner.
func Of(owner string) []Proc {
	var out []Proc
	for _, p := range Marked() {
		if p.Owner == owner {
			out = append(out, p)
		}
	}
	return out
}

// Orphans lists marked processes whose test run is no longer running.
func Orphans() []Proc {
	var out []Proc
	for _, p := range Marked() {
		pid, _, ok := strings.Cut(p.Owner, ":")
		n, err := strconv.Atoi(pid)
		if !ok || err != nil || Owner(n) != p.Owner {
			out = append(out, p)
		}
	}
	return out
}

// WaitGone waits up to d for owner's processes to exit (a killed tmux server
// takes a moment to hang up its panes) and returns those still running.
func WaitGone(owner string, d time.Duration) []Proc {
	for end := time.Now().Add(d); ; time.Sleep(100 * time.Millisecond) {
		left := Of(owner)
		if len(left) == 0 || time.Now().After(end) {
			return left
		}
	}
}

// Reap stops ps: SIGTERM, then SIGKILL for any left after two seconds. A
// fleet-test-* tmux server's socket file goes with it.
func Reap(ps []Proc) {
	if len(ps) == 0 {
		return
	}
	var socks []string
	for _, p := range ps {
		if s := testSocket(p.PID); s != "" {
			socks = append(socks, s)
		}
		sysx.Kill(p.PID, syscall.SIGTERM)
	}
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		alive := false
		for _, p := range ps {
			alive = alive || running(p.PID)
		}
		if !alive {
			break
		}
	}
	for _, p := range ps {
		if running(p.PID) {
			sysx.Kill(p.PID, syscall.SIGKILL)
		}
	}
	for _, s := range socks {
		os.Remove(s)
	}
}

// testSocket returns the socket file of a `tmux -L fleet-test-*` server, or "".
func testSocket(pid int) string {
	cmd, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return ""
	}
	args := strings.Split(strings.TrimRight(string(cmd), "\x00"), "\x00")
	if filepath.Base(args[0]) != "tmux" {
		return ""
	}
	name := ""
	for i, a := range args {
		if a == "-L" && i+1 < len(args) {
			name = args[i+1]
		}
	}
	if !strings.HasPrefix(name, "fleet-test-") || strings.ContainsRune(name, '/') {
		return ""
	}
	dir := envOf(pid, "TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), name)
}

// Eventually polls cond every 20ms until it holds, and fails the test with
// what after timeout. Use it instead of a fixed sleep when a test waits for
// a pane to draw or a process to act: a sleep long enough for a loaded CI
// race job is wasted everywhere else, and one short enough is a flake.
func Eventually(t interface {
	Helper()
	Fatalf(string, ...any)
}, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(timeout); !cond(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out after %v waiting for %s", timeout, what)
			return
		}
	}
}
