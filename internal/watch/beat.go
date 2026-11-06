package watch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// StaleAfter is how long a heartbeat counts as live. The watcher writes one
// a second, so anything older means it died or is wedged, and whoever asked
// should refresh for itself.
const StaleAfter = 5 * time.Second

// Beat is ~/.fleet/watch.json: who is watching, since when, when it last
// said so, and when it last refreshed the board without error. A watcher
// whose refreshes fail keeps breathing but stops being useful, so OK is what
// other processes trust.
type Beat struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	At      time.Time `json:"at"`
	OK      time.Time `json:"ok"`
}

// BeatPath is the heartbeat file under home.
func BeatPath(home string) string { return filepath.Join(home, "watch.json") }

// WriteBeat stores the heartbeat (atomic rename: a reader never sees half).
func WriteBeat(home string, b Beat) error {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	tmp := BeatPath(home) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, BeatPath(home))
}

// ReadBeat returns the last heartbeat written under home.
func ReadBeat(home string) (Beat, bool) {
	data, err := os.ReadFile(BeatPath(home))
	if err != nil {
		return Beat{}, false
	}
	var b Beat
	if json.Unmarshal(data, &b) != nil || b.PID == 0 {
		return Beat{}, false
	}
	return b, true
}

// DropBeat removes the heartbeat, so the next command knows at once that
// nobody is watching. A watcher only drops its own: two of them on one home
// must not delete each other's on the way out.
func DropBeat(home string) {
	if b, ok := ReadBeat(home); ok && b.PID != 0 && b.PID != os.Getpid() {
		return
	}
	_ = os.Remove(BeatPath(home))
}

// held is the open watch.lock of each home this process has claimed. The
// flock lives as long as the file stays open: the kernel drops it when the
// process dies or execs (Go opens files close-on-exec), so nothing has to
// clean it up and a recycled pid can never hold it.
var (
	heldMu sync.Mutex
	held   = map[string]*os.File{}
)

// Claim takes ownership of the watcher role on home for this process: an
// exclusive flock on watch.lock, kept until Release or exit (ADR-62). It
// fails while anyone else holds it, which is what stops two `fleet up`s from
// racing into two watchers. A watcher from before ADR-62 holds no flock, so
// its fresh heartbeat from a live pid also counts as held.
func Claim(home string, now time.Time) (Beat, error) {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return Beat{}, err
	}
	f, err := os.OpenFile(lockPath(home), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return Beat{}, err
	}
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// Running probes with a brief shared lock; give such a probe time to let go.
		time.Sleep(50 * time.Millisecond)
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	}
	if err != nil {
		f.Close()
		if pid, ok := lockPID(home); ok {
			return Beat{}, fmt.Errorf("another watcher is running (pid %d)", pid)
		}
		return Beat{}, fmt.Errorf("another watcher is running")
	}
	if b, ok := ReadBeat(home); ok && b.PID != os.Getpid() && fresh(b.At, now) && alive(b.PID) {
		f.Close()
		return Beat{}, fmt.Errorf("another watcher is running (pid %d)", b.PID)
	}
	pid := []byte(strconv.Itoa(os.Getpid()) + "\n")
	if _, err := f.WriteAt(pid, 0); err == nil {
		_ = f.Truncate(int64(len(pid)))
	}
	heldMu.Lock()
	if old := held[home]; old != nil {
		old.Close()
	}
	held[home] = f
	heldMu.Unlock()
	b := Beat{PID: os.Getpid(), Started: now, At: now, OK: now}
	if err := WriteBeat(home, b); err != nil {
		return Beat{}, err
	}
	return b, nil
}

// Release drops this process's claim. watch.lock itself stays: removing a
// lock file another process may have just opened would let a third one lock
// a fresh inode beside it.
func Release(home string) {
	heldMu.Lock()
	f := held[home]
	delete(held, home)
	heldMu.Unlock()
	if f == nil {
		return
	}
	DropBeat(home)
	f.Close()
}

// Clear removes a dead watcher's heartbeat, whoever is asking. It is for a
// command that has just found the pid gone; a held claim, or a live older
// watcher's fresh heartbeat, stays.
func Clear(home string) {
	if _, ok := flockHolder(home); ok {
		return
	}
	if b, ok := ReadBeat(home); ok && b.PID != os.Getpid() && fresh(b.At, time.Now()) && alive(b.PID) {
		return
	}
	_ = os.Remove(BeatPath(home))
}

// Running reports the pid of a watcher process that still exists, live or
// wedged, so `fleet watch --stop` can signal one whose refreshes are failing
// instead of calling it absent. The flock decides; an older watcher without
// one is recognised by a fresh heartbeat from a live pid, never by a pid
// alone, which may have been recycled.
func Running(home string) (int, bool) {
	if pid, ok := flockHolder(home); ok {
		return pid, pid > 0
	}
	if b, ok := ReadBeat(home); ok && fresh(b.At, time.Now()) && alive(b.PID) {
		return b.PID, true
	}
	return 0, false
}

// flockHolder reports whether watch.lock is flocked by anyone, and the pid
// written in it.
func flockHolder(home string) (int, bool) {
	f, err := os.Open(lockPath(home))
	if err != nil {
		return 0, false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err == nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return 0, false
	}
	pid, _ := lockPID(home)
	return pid, true
}

// StaleExe reports whether process pid runs another binary than exe: one
// that has been deleted or replaced since it started. `fleet up` restarts a
// watcher like that, which is how a watcher from before ADR-62 (which does
// not re-exec itself) gets replaced.
func StaleExe(pid int, exe string) bool {
	running, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		return false
	}
	want, err := os.Stat(exe)
	if err != nil {
		return false
	}
	return !sameFile(running, want)
}

// lockPID is the pid written into the lock file.
func lockPID(home string) (int, bool) {
	b, err := os.ReadFile(lockPath(home))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func lockPath(home string) string { return filepath.Join(home, "watch.lock") }

// Live reports whether a watcher is running and keeping the board fresh: a
// heartbeat and a successful refresh younger than StaleAfter, from a process
// that still exists. A watcher whose refreshes keep failing is not live, so
// the overview goes back to refreshing for itself instead of showing a board
// nobody is updating.
func Live(home string, now time.Time) bool {
	b, ok := ReadBeat(home)
	if !ok || !fresh(b.At, now) || !fresh(b.OK, now) {
		return false
	}
	return alive(b.PID)
}

// fresh reports whether t is within StaleAfter of now, in either direction
// (a clock change must not make a dead watcher look live forever).
func fresh(t, now time.Time) bool {
	return !t.IsZero() && now.Sub(t) <= StaleAfter && !t.After(now.Add(StaleAfter))
}

// alive reports whether pid exists. Signal 0 only tests for the process; it
// never touches it.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// EPERM means the process exists but belongs to someone else, which
	// still counts as alive: only "no such process" means it is gone.
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
