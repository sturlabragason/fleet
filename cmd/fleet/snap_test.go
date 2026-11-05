package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckState(t *testing.T) {
	for _, s := range []string{"idle", "waiting-approval", "error"} {
		if err := checkState(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	for _, s := range []string{"busy", "", "idle2", "idle-../../../tmp/x", "Idle", "idle/x"} {
		if err := checkState(s); err == nil {
			t.Errorf("%q should be rejected", s)
		}
	}
}

func TestWriteSnapAndDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, testdataDir, "claude")
	if _, err := snapDir(root); err == nil {
		t.Error("missing testdata should be an error")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := snapDir(root)
	if err != nil || got != filepath.Join(root, testdataDir) {
		t.Fatalf("snapDir: %q %v", got, err)
	}
	p := filepath.Join(dir, "idle.txt")
	if err := writeSnap(p, "> hi   \n\u00a0keep\t\n\n\n"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "> hi\n\u00a0keep\n" {
		t.Errorf("got %q", string(b))
	}
	if err := writeSnap(p, "  \n\n"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("empty pane: %v", err)
	}
}
