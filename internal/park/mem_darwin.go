package park

import "syscall"

// sysMem answers memInfo's MemTotal and MemAvailable from sysctl. Available
// is the kernel's own measure, kern.memorystatus_level: the percentage of
// memory free under its pressure accounting (what memory_pressure(1) prints
// as "System-wide memory free percentage"). It counts what can be reclaimed
// without swapping, as MemAvailable does on Linux; darwin's free page count
// alone reads near zero on any warm machine.
func sysMem(key string) (uint64, bool) {
	total, ok := sysctlUint64("hw.memsize")
	switch {
	case !ok:
		return 0, false
	case key == "MemTotal:":
		return total, true
	case key == "MemAvailable:":
		pct, err := syscall.SysctlUint32("kern.memorystatus_level")
		if err != nil || pct > 100 {
			return 0, false
		}
		return total / 100 * uint64(pct), true
	}
	return 0, false
}

// sysctlUint64 reads a 64-bit sysctl. syscall.Sysctl returns the raw value
// as a string with one trailing NUL byte dropped, which for a little-endian
// integer is its zero top byte: pad it back.
func sysctlUint64(name string) (uint64, bool) {
	s, err := syscall.Sysctl(name)
	if err != nil || len(s) == 0 || len(s) > 8 {
		return 0, false
	}
	var v uint64
	for i := len(s) - 1; i >= 0; i-- {
		v = v<<8 | uint64(s[i])
	}
	return v, v > 0
}
