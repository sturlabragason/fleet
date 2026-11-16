//go:build !darwin

package park

// sysMem has nothing to add where memory is read from /proc/meminfo.
func sysMem(string) (uint64, bool) { return 0, false }
