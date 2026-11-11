// Package sysx holds the few OS calls fleet makes that differ between Unix
// and Windows: advisory file locks, signalling a process, starting a
// detached child or a killable process group, and replacing this process.
// Unix keeps the syscall behaviour; Windows gets the nearest equivalent.
package sysx

// Flock operations, as for flock(2).
const (
	LockSh = 1
	LockEx = 2
	LockNB = 4
	LockUn = 8
)
