package prefs

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/conf"
)

// Memory is the [memory] section: the RAM guard (DESIGN §7, ADR-44).
type Memory struct {
	// Guard turns auto-parking on; guard = false switches it off.
	Guard bool
	// MinFree is the free-memory floor in bytes. Under it the guard parks
	// the longest-idle agent and new spawns queue instead of starting.
	MinFree uint64
	// Every is the least time between two guard actions, so freed memory
	// is seen before the next agent is parked.
	Every time.Duration
	// ParkIdle parks an agent once it has been idle this long, with memory
	// to spare or not; 0 (the default) leaves idle agents running.
	ParkIdle time.Duration
}

// Defaults for [memory] (DESIGN §7).
const (
	DefaultMinFree  = 4 << 30 // 4 GiB
	DefaultMemEvery = 30 * time.Second
)

// DefaultMemory is the guard without a config file: on, 4 GiB, every 30s.
func DefaultMemory() Memory {
	return Memory{Guard: true, MinFree: DefaultMinFree, Every: DefaultMemEvery}
}

// Memory is [memory]: the RAM guard settings. It is read live, so a change
// takes effect without restarting fleet.
func (p *Prefs) Memory() Memory {
	if p == nil {
		return DefaultMemory()
	}
	return p.memory
}

func applyMemory(m Memory, sec map[string]conf.Value, path string) (Memory, error) {
	if v, ok := conf.Bool(sec, "guard"); ok {
		m.Guard = v
	}
	if v := strings.TrimSpace(conf.Str(sec, "min_free")); v != "" {
		n, err := ParseSize(v)
		if err != nil {
			return m, fmt.Errorf("%s: min_free %q: %w", path, v, err)
		}
		m.MinFree = n
	}
	if v, ok := conf.Dur(sec, "check_every"); ok {
		if v <= 0 {
			return m, fmt.Errorf("%s: check_every: want a duration > 0", path)
		}
		m.Every = v
	}
	if v, ok := conf.Dur(sec, "park_idle"); ok {
		if v < 0 {
			return m, fmt.Errorf("%s: park_idle: want a duration >= 0", path)
		}
		m.ParkIdle = v
	}
	return m, nil
}

// ParseSize reads a byte size: a plain number of bytes, or a number with a
// KB/MB/GB/TB suffix (KiB/MiB/… and a bare K/M/G/T are the same thing;
// fleet counts in powers of 1024, as free(1) and /proc/meminfo do).
func ParseSize(s string) (uint64, error) {
	t := strings.TrimSpace(s)
	i := len(t)
	for i > 0 && (t[i-1] < '0' || t[i-1] > '9') && t[i-1] != '.' {
		i--
	}
	num, unit := strings.TrimSpace(t[:i]), strings.ToUpper(strings.TrimSpace(t[i:]))
	v, err := strconv.ParseFloat(num, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("want a size like 4GB, 512MB or a number of bytes")
	}
	mul := map[string]float64{"": 1, "B": 1,
		"K": 1 << 10, "KB": 1 << 10, "KIB": 1 << 10,
		"M": 1 << 20, "MB": 1 << 20, "MIB": 1 << 20,
		"G": 1 << 30, "GB": 1 << 30, "GIB": 1 << 30,
		"T": 1 << 40, "TB": 1 << 40, "TIB": 1 << 40}[unit]
	if mul == 0 {
		return 0, fmt.Errorf("unknown unit %q: want B, KB, MB, GB or TB", unit)
	}
	if n := v * mul; n <= math.MaxUint64 {
		return uint64(n), nil
	}
	return 0, fmt.Errorf("too large")
}
