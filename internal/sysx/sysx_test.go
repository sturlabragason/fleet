package sysx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFlockExcludesASecondHandle(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	a, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := os.OpenFile(p, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := Flock(a, LockEx); err != nil {
		t.Fatal(err)
	}
	if Flock(b, LockEx|LockNB) == nil {
		t.Fatal("second exclusive lock succeeded while the first is held")
	}
	if err := Flock(a, LockUn); err != nil {
		t.Fatal(err)
	}
	if err := Flock(b, LockEx|LockNB); err != nil {
		t.Fatalf("lock after unlock: %v", err)
	}
	Flock(b, LockUn)
}

func TestKillZeroSeesThisProcess(t *testing.T) {
	if err := Kill(os.Getpid(), 0); err != nil {
		t.Fatalf("Kill(self, 0) = %v", err)
	}
}
