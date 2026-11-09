package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/proc"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// kidProfile starts a child that ignores SIGHUP, like a CLI's helper
// server, prints its pid and then waits like an idle agent.
const kidProfile = `[kid]
command = "sh"
args = ["-c", "trap '' HUP; sleep 300 & echo kid=$!; echo '\u2714 Done'; cat", "sh"]
launch_id = ["started-{id}"]
resume = ["resumed-{id}"]
patterns = "fake"
`

func kidFleet(t *testing.T) *Fleet {
	t.Helper()
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(kidProfile), 0o644)
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "nonexistent.toml"))
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

var kidRe = regexp.MustCompile(`kid=(\d+)`)

// spawnKid starts agent name and returns its child's pid.
func spawnKid(t *testing.T, f *Fleet, name string) int {
	t.Helper()
	if _, err := f.Spawn(name, "kid", f.Home, ""); err != nil {
		t.Fatal(err)
	}
	pane := waitPane(t, f, name)
	for i := 0; i < 50; i++ {
		out, _ := f.T.Capture(pane, 20)
		if m := kidRe.FindStringSubmatch(out); m != nil {
			pid, _ := strconv.Atoi(m[1])
			t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
			return pid
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("child pid never shown")
	return 0
}

func TestParkAndCloseKillChildProcesses(t *testing.T) {
	f := kidFleet(t)
	parked := spawnKid(t, f, "k1")
	closed := spawnKid(t, f, "k2")
	setState(t, f, "k1", "idle", time.Now())
	if err := f.Park("k1", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.Close("k2"); err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int{parked, closed} {
		if proc.Running(pid) {
			t.Errorf("child %d outlived its agent", pid)
		}
	}
}
