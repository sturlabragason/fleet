package proc

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/sysx"
)

// darwin has no /proc. The process table comes from ps(1), the system's own
// reader of it (sysctl kern.proc plus libproc, which need cgo), one call per
// table: what /proc/<pid>/stat gives on Linux is in psColumns.
//
// What darwin can't give fleet:
//   - another process's environment (ps -E shows nothing under SIP), so
//     Info.Owner is always "": the guard and doctor find no orphans by tag,
//     only by tree (the guard's memory and CPU caps still work);
//   - per-process swap, so Swap is 0;
//   - open file descriptors without lsof, so Docs reads argv only.

// OwnerTags reports whether Scan can read Info.Owner: not on darwin.
const OwnerTags = false

// psColumns is the ps(1) format the darwin process table reads: everything
// /proc/<pid>/stat gives on Linux. lstart is five words under LC_ALL=C and
// comm, last, is the executable's path, which may hold spaces.
const psColumns = "pid=,ppid=,stat=,tty=,rss=,time=,lstart=,comm="

// psTimeout bounds one ps call; it reads a table, it never blocks.
const psTimeout = 5 * time.Second

// runPS runs ps with args under LC_ALL=C (lstart's words) and returns its
// output. ps exits 1 when -p names no live process; its output, empty, is
// still the answer then.
func runPS(args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		logx.For("proc").Debug("ps failed", "args", args, "err", err)
	}
	return string(out)
}

// rows reads the table for pids, or for every process when none are given.
func rows(pids ...int) []psRow {
	if len(pids) == 0 {
		return parsePS(runPS("-axww", "-o", psColumns))
	}
	ids := make([]string, len(pids))
	for i, p := range pids {
		ids[i] = strconv.Itoa(p)
	}
	return parsePS(runPS("-ww", "-o", psColumns, "-p", strings.Join(ids, ",")))
}

// row is pid's line of the table.
func row(pid int) (psRow, bool) {
	if pid <= 0 {
		return psRow{}, false
	}
	// Most callers poll processes that are about to go: skip ps for a pid
	// that is already gone. EPERM is another user's live process.
	if err := sysx.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return psRow{}, false
	}
	for _, r := range rows(pid) {
		if r.PID == pid {
			return r, true
		}
	}
	return psRow{}, false
}

// start is a row's identity stamp for Proc.Start: its start in Unix
// seconds (Linux uses clock ticks since boot; neither leaves its platform).
func (r psRow) start() uint64 { return uint64(max(r.Start.Unix(), 0)) }

// stat reads the parent pid, state and start time of pid.
func stat(pid int) (ppid int, state byte, start uint64, ok bool) {
	r, ok := row(pid)
	if !ok {
		return 0, 0, 0, false
	}
	return r.PPID, r.State[0], r.start(), true
}

// children maps every process's parent pid to it.
func children() map[int][]Proc {
	kids := map[int][]Proc{}
	for _, r := range rows() {
		kids[r.PPID] = append(kids[r.PPID], Proc{r.PID, r.start()})
	}
	return kids
}

// Scan lists every live process. Owner is always "" on darwin (see above).
func Scan(string) []Info {
	now := time.Now()
	var out []Info
	for _, r := range rows() {
		if r.State[0] == 'Z' {
			continue
		}
		out = append(out, Info{Proc: Proc{r.PID, r.start()}, PPID: r.PPID, Comm: r.Comm(), Ticks: r.Ticks, RSS: r.RSS,
			Age: now.Sub(r.Start), Daemon: r.Daemon()})
	}
	return out
}

// Swap is 0: darwin does not report swap per process.
func Swap(int) uint64 { return 0 }

// args is pid's command line as ps prints it: argv joined by spaces.
func args(pid int) string {
	return strings.TrimSpace(runPS("-ww", "-o", "args=", "-p", strconv.Itoa(pid)))
}

// GUI reports whether pid is a desktop app an agent opened for the user
// (see the Linux GUI): LibreOffice by name, anything else by running from
// an app bundle's Contents/MacOS. A bundle inside a framework is not one:
// Homebrew's and python.org's python run as Python.framework/…/Python.app.
// A --headless process is not one either.
func GUI(pid int) bool {
	path := strings.TrimSpace(runPS("-ww", "-o", "comm=", "-p", strconv.Itoa(pid)))
	if path == "" {
		return false
	}
	app := strings.Contains(path, ".app/Contents/MacOS/") && !strings.Contains(path, ".framework/")
	if !app && !Office(psRow{Path: path}.Comm()) {
		return false
	}
	return !strings.Contains(args(pid), "--headless")
}

// Docs lists the documents pid was started with, sorted, without
// duplicates. darwin reads argv only (see above).
func Docs(pid int) []string {
	var out []string
	for a := range strings.FieldsSeq(args(pid)) {
		if docExts[strings.ToLower(filepath.Ext(a))] && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	slices.Sort(out)
	return out
}

// Cmdline is one process's command line.
type Cmdline struct {
	PID  int
	Argv []string // argv as ps prints it, split on spaces
}

// Cmdlines lists every process's command line, for the session-in-use check
// (internal/sessions), in one ps call.
func Cmdlines() []Cmdline {
	var out []Cmdline
	for line := range strings.SplitSeq(runPS("-axww", "-o", "pid=,args="), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if pid, err := strconv.Atoi(f[0]); err == nil {
			out = append(out, Cmdline{pid, f[1:]})
		}
	}
	return out
}

// Parent is pid's parent pid, 0 when unknown.
func Parent(pid int) int {
	r, ok := row(pid)
	if !ok {
		return 0
	}
	return r.PPID
}

// SelfExe is the path to stat for the running binary: os.Executable, since
// darwin has no /proc/self/exe. Stat it at startup: an install that renames
// a new binary over the path is seen as a change from then on.
func SelfExe() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return exe
}

// RunsExe reports whether pid runs the binary at exe as it is now; ok is
// false when either can't be read. darwin can't stat another process's
// running file, so it is the same path, not modified since pid started
// (with a second's slack: ps gives the start to the second).
func RunsExe(pid int, exe string) (runs, ok bool) {
	r, found := row(pid)
	want, err := os.Stat(exe)
	if !found || err != nil {
		return false, false
	}
	return samePath(r.Path, exe) && !want.ModTime().After(r.Start.Add(time.Second)), true
}

func samePath(a, b string) bool {
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return a == b
}
