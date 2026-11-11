package main

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// A running fleet whose tmux options predate this build gets them from
// hotApply, once: an unchanged stamp is left alone.
func TestHotApplyReappliesChrome(t *testing.T) {
	t.Setenv("FLEET_CONFIG", filepath.Join(t.TempDir(), "no-config.toml"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := fleet.New(tc, t.TempDir(), "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if hotApply(f) {
		t.Fatal("applied without a fleet session")
	}
	if _, err := tc.Run("new-session", "-d", "-s", fleet.Session, "sleep 600"); err != nil {
		t.Fatal(err)
	}
	tc.Run("set-option", "-g", chromeOption, "old-build")
	tc.Run("set-option", "-g", "status-right-length", "7")
	if !hotApply(f) {
		t.Fatal("stale stamp not re-applied")
	}
	if got, _ := tc.Run("show-options", "-gqv", "status-right-length"); got != strconv.Itoa(statusRightLen) {
		t.Fatalf("status-right-length %q", got)
	}
	if got, _ := tc.Run("show-options", "-sqv", "terminal-features[91]"); got != "xterm*:hyperlinks" {
		t.Fatalf("terminal-features[91] %q", got)
	}
	if hotApply(f) {
		t.Fatal("re-applied an unchanged stamp")
	}
}

// The release path end to end: a resident watcher on a running fleet, the
// binary replaced on disk, and no `fleet up`. The watcher must re-exec into
// the new binary (same pid) and the new image re-apply the tmux options. The
// copy has the same chrome as the old build, so the stale stamp stands in for
// a release that changed an option.
func TestBinaryBumpHotApplies(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the fleet binary")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "fleet")
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	home := filepath.Join(dir, "home")
	tmpdir := filepath.Join(dir, "tmux")
	os.MkdirAll(home, 0o700)
	os.MkdirAll(tmpdir, 0o700)
	// The fleet binary always uses -L fleet; a private TMUX_TMPDIR makes that
	// a server of this test's own. The test itself only ever names it by path.
	sock := filepath.Join(tmpdir, fmt.Sprintf("tmux-%d", os.Getuid()), fleet.Socket)
	os.MkdirAll(filepath.Dir(sock), 0o700)
	tm := func(args ...string) string {
		out, _ := exec.Command("tmux", append([]string{"-u", "-S", sock, "-f", "/dev/null"}, args...)...).Output()
		return strings.TrimSpace(string(out))
	}
	var env []string
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if k == "TMUX" || k == "TMUX_PANE" || k == "HOME" || k == "XDG_CONFIG_HOME" || k == "TMUX_TMPDIR" || strings.HasPrefix(k, "FLEET_") {
			continue
		}
		env = append(env, e)
	}
	env = append(env, "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"FLEET_HOME="+filepath.Join(home, ".fleet"), "FLEET_CONFIG="+filepath.Join(home, "no-config.toml"),
		"TMUX_TMPDIR="+tmpdir)
	start := exec.Command("tmux", "-u", "-S", sock, "-f", "/dev/null", "new-session", "-d", "-s", fleet.Session, "sleep 600")
	start.Env = env
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("tmux: %v\n%s", err, out)
	}
	t.Cleanup(func() { tm("kill-server") })

	w := exec.Command(exe, "watch", "--tick", "50ms")
	w.Env = env
	w.Stdout, w.Stderr = io.Discard, io.Discard
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	pid := w.Process.Pid
	done := make(chan struct{})
	go func() { w.Wait(); close(done) }()
	t.Cleanup(func() {
		syscall.Kill(pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			w.Process.Kill()
			<-done
		}
	})
	want := strconv.Itoa(statusRightLen)
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
			select {
			case <-done:
				t.Fatalf("watcher exited while waiting for %s", what)
			default:
			}
			if ok() {
				return
			}
		}
		lg, _ := os.ReadFile(filepath.Join(home, ".fleet", "logs", "fleet.log"))
		t.Fatalf("timed out waiting for %s\n%s", what, lg)
	}
	// The stamp is written last, so the first apply is over once it is set.
	waitFor("the first apply", func() bool { return tm("show-options", "-gqv", chromeOption) != "" })

	tm("set-option", "-g", chromeOption, "old-build")
	tm("set-option", "-g", "status-right-length", "7")
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe+".new", b, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(exe+".new", exe); err != nil {
		t.Fatal(err)
	}
	bumped, _ := os.Stat(exe)
	waitFor("the re-exec", func() bool {
		fi, err := os.Stat(fmt.Sprintf("/proc/%d/exe", pid))
		return err == nil && os.SameFile(fi, bumped)
	})
	waitFor("the re-apply", func() bool { return tm("show-options", "-gqv", chromeOption) != "old-build" })
	if got := tm("show-options", "-gqv", "status-right-length"); got != want {
		t.Fatalf("status-right-length %q after the re-apply", got)
	}
}
