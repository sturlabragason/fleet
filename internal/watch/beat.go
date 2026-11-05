package watch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// Claim takes ownership of the heartbeat for this process. It fails while
// the lock names a process that still exists, which is what stops two
// `fleet up`s racing into two watchers on one home. Freshness is not the
// test: a watcher wedged in a capture, or one whose refreshes are failing,
// is still the watcher, and a second one would only double the scraping.
func Claim(home string, now time.Time) (Beat, error) {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return Beat{}, err
	}
	f, err := os.OpenFile(lockPath(home), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	switch {
	case err == nil:
		fmt.Fprintf(f, "%d\n", os.Getpid())
		f.Close()
	case !os.IsExist(err):
		return Beat{}, err
	default:
		if pid, ok := lockPID(home); ok && pid != os.Getpid() && alive(pid) {
			return Beat{}, fmt.Errorf("another watcher is running (pid %d)", pid)
		}
		// The lock is stale (crash, reboot, recycled pid): take it over.
		if err := writeLock(home); err != nil {
			return Beat{}, err
		}
	}
	b := Beat{PID: os.Getpid(), Started: now, At: now, OK: now}
	if err := WriteBeat(home, b); err != nil {
		return Beat{}, err
	}
	return b, nil
}

// Release drops this process's claim.
func Release(home string) {
	if b, ok := ReadBeat(home); ok && b.PID != 0 && b.PID != os.Getpid() {
		return
	}
	_ = os.Remove(lockPath(home))
	DropBeat(home)
}

// Clear removes a dead watcher's claim, whoever is asking. It is for a
// command that has just found the pid gone; a live watcher's claim stays.
func Clear(home string) {
	if pid, ok := lockPID(home); ok && pid != os.Getpid() && alive(pid) {
		return
	}
	if b, ok := ReadBeat(home); ok && b.PID != 0 && b.PID != os.Getpid() && alive(b.PID) {
		return
	}
	_ = os.Remove(lockPath(home))
	_ = os.Remove(BeatPath(home))
}

// Running reports the pid of a watcher process that still exists, live or
// wedged, so `fleet watch --stop` can signal one whose refreshes are failing
// instead of calling it absent.
func Running(home string) (int, bool) {
	if b, ok := ReadBeat(home); ok && b.PID != 0 && alive(b.PID) {
		return b.PID, true
	}
	if pid, ok := lockPID(home); ok && alive(pid) {
		return pid, true
	}
	return 0, false
}

func writeLock(home string) error {
	return os.WriteFile(lockPath(home), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
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
