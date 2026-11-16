package park

import (
	"path/filepath"
	"testing"
)

// Without /proc/meminfo, darwin reads total and available memory from sysctl.
func TestMemFromSysctl(t *testing.T) {
	old := MemInfo
	MemInfo = filepath.Join(t.TempDir(), "no-meminfo")
	t.Cleanup(func() { MemInfo = old })
	total, err := Total()
	if err != nil || total < 1<<30 {
		t.Fatalf("Total = %d, %v", total, err)
	}
	free, err := Free()
	if err != nil || free == 0 || free > total {
		t.Fatalf("Free = %d (total %d), %v", free, total, err)
	}
}
