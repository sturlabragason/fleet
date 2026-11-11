package testenv

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) { os.Exit(Main(m)) }

func sleeper(t *testing.T, owner string) *exec.Cmd {
	t.Helper()
	if _, err := os.Stat("/proc/self/environ"); err != nil {
		t.Skip("no /proc")
	}
	cmd := exec.Command("sleep", "60")
	cmd.Env = append(os.Environ(), Env+"="+owner)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	return cmd
}

func pids(ps []Proc) []int {
	var out []int
	for _, p := range ps {
		out = append(out, p.PID)
	}
	return out
}

func TestOrphanOfAGoneRunIsReaped(t *testing.T) {
	// pid 1 is running, but not since tick 0: the run that marked this is gone.
	cmd := sleeper(t, "1:0")
	orphans := Orphans()
	if !slices.Contains(pids(orphans), cmd.Process.Pid) {
		t.Fatalf("orphan %d not found in %v", cmd.Process.Pid, orphans)
	}
	Reap([]Proc{{PID: cmd.Process.Pid}})
	cmd.Wait()
	if running(cmd.Process.Pid) {
		t.Fatal("orphan still running after Reap")
	}
}

func TestLiveRunIsNotAnOrphan(t *testing.T) {
	own := Owner(os.Getpid())
	cmd := sleeper(t, own)
	if slices.Contains(pids(Orphans()), cmd.Process.Pid) {
		t.Fatal("a live run's process was taken for an orphan")
	}
	if !slices.Contains(pids(Of(own)), cmd.Process.Pid) {
		t.Fatal("own process not listed")
	}
}

type leaky struct {
	t   *testing.T
	cmd *exec.Cmd
}

func (l *leaky) Run() int {
	l.cmd = exec.Command("sleep", "60")
	if err := l.cmd.Start(); err != nil {
		l.t.Fatal(err)
	}
	return 0
}

func TestMainFailsAndKillsALeak(t *testing.T) {
	if _, err := os.Stat("/proc/self/environ"); err != nil {
		t.Skip("no /proc")
	}
	l := &leaky{t: t}
	if code := Main(l); code != 1 {
		t.Fatalf("Main returned %d with a leaked process, want 1", code)
	}
	l.cmd.Wait()
	if running(l.cmd.Process.Pid) {
		t.Fatal("leaked process still running")
	}
}

type fakeT struct{ failed string }

func (*fakeT) Helper()                          {}
func (f *fakeT) Fatalf(format string, a ...any) { f.failed = fmt.Sprintf(format, a...) }

func TestEventually(t *testing.T) {
	n := 0
	Eventually(t, time.Second, "third call", func() bool { n++; return n == 3 })
	ft := &fakeT{}
	Eventually(ft, 50*time.Millisecond, "never", func() bool { return false })
	if !strings.Contains(ft.failed, "waiting for never") {
		t.Errorf("failure %q", ft.failed)
	}
}
