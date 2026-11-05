package park

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// MemInfo is the file free memory is read from; tests replace it.
var MemInfo = "/proc/meminfo"

// Free is the memory the machine can still hand out without swapping, in
// bytes: MemAvailable from /proc/meminfo, which already accounts for
// reclaimable cache (MemFree alone reads far too low on a warm machine).
func Free() (uint64, error) {
	b, err := os.ReadFile(MemInfo)
	if err != nil {
		return 0, err
	}
	for _, l := range strings.Split(string(b), "\n") {
		v, ok := strings.CutPrefix(l, "MemAvailable:")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			break
		}
		kb, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			break
		}
		return kb * 1024, nil
	}
	return 0, fmt.Errorf("%s: no MemAvailable line", MemInfo)
}

// Bytes formats a byte count the way the guard reports it: "3.2 GiB".
func Bytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v, exp := float64(n)/unit, 0
	for v >= unit && exp < 3 {
		v, exp = v/unit, exp+1
	}
	return fmt.Sprintf("%.1f %s", v, [...]string{"KiB", "MiB", "GiB", "TiB"}[exp])
}
