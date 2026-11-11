package logx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Two processes share fleet.log: the one that finds it past MaxSize rotates
// it, the other follows to the new file, and only Keep old files stay.
func TestRotationIsShared(t *testing.T) {
	defer func(m int64, c time.Duration) { MaxSize, checkEvery = m, c }(MaxSize, checkEvery)
	MaxSize, checkEvery = 100, 0
	path := filepath.Join(t.TempDir(), "fleet.log")
	a, err := openRotator(path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := openRotator(path)
	line := []byte(strings.Repeat("x", 60) + "\n")
	for i := 0; i < 8; i++ {
		a.Write(line)
	}
	b.Write([]byte("from b\n"))
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "from b") || len(data) > int(MaxSize)+len(line) {
		t.Errorf("fleet.log after rotations: %d bytes %q", len(data), data)
	}
	for _, n := range []string{".1", ".2"} {
		if _, err := os.Stat(path + n); err != nil {
			t.Errorf("no %s: %v", n, err)
		}
	}
	if _, err := os.Stat(path + ".3"); err == nil {
		t.Error("kept more than Keep old logs")
	}
}
