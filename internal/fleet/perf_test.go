package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/tmux"
)

func TestSpawnAddsConfigEnv(t *testing.T) {
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[envp]
command = "sh"
args = ["-c", "echo \"GF=$GOFLAGS GM=$GOMAXPROCS KEEP=$KEEP\"; cat"]
patterns = "fake"
env = { KEEP = "profile", GOMAXPROCS = "9" }
`), 0o644)
	cfg := filepath.Join(home, "config.toml")
	os.WriteFile(cfg, []byte("[defaults]\nenv = { GOFLAGS = \"-p=4\", GOMAXPROCS = \"4\" }\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Spawn("e1", "envp", home, ""); err != nil {
		t.Fatal(err)
	}
	var out string
	for range 30 {
		out, _ = f.Peek("e1", 0)
		if strings.Contains(out, "GF=") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(out, "GF=-p=4 GM=4 KEEP=profile") {
		t.Errorf("pane env: %q", out)
	}
}

func TestSkipCaptureOnlyOffWallAndThrottled(t *testing.T) {
	f := &Fleet{OffWallEvery: 5 * time.Second}
	now := time.Now()
	a := &board.Agent{Name: "a", Status: "working"}
	if f.skipCapture(a, true, false, now) {
		t.Error("first look must capture")
	}
	if !f.skipCapture(a, true, false, now.Add(time.Second)) {
		t.Error("off-wall within 5s should skip")
	}
	if f.skipCapture(a, true, true, now.Add(time.Second)) {
		t.Error("on-wall must always capture")
	}
	if f.skipCapture(a, false, false, now.Add(2*time.Second)) {
		t.Error("dead/missing pane must be classified")
	}
	if f.skipCapture(a, true, false, now.Add(10*time.Second)) {
		t.Error("off-wall after 5s must capture")
	}
	if (&Fleet{}).skipCapture(a, true, false, now) {
		t.Error("OffWallEvery 0 never skips")
	}
}
