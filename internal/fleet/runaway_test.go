package fleet

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/proc"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/sysx"
)

// leaker starts a helper that leaves the pane's process tree at once (the
// subshell exits, so sleep is reparented away) and ignores the pane's hangup, like a CLI daemon that
// outlives a close (#48), then waits like an idle agent.
const leaker = `
[leak]
command = "sh"
args = ["-c", "((trap '' HUP TERM; exec sleep 300) & echo orphan=$!; (trap '' HUP TERM; exec setsid sleep 301) & echo daemon=$!; (trap '' HUP TERM; env -u FLEET_OWNER sleep 302 & echo user=$!; exec sleep 303) & echo parent=$!); echo '\u2714 Done'; cat", "sh"]
launch_id = ["started-{id}"]
resume = ["resumed-{id}"]
patterns = "fake"

[spin]
command = "sh"
args = ["-c", "echo '\u2714 Done'; while :; do :; done", "sh"]
launch_id = ["started-{id}"]
resume = ["resumed-{id}"]
patterns = "fake"
`

// guardNow sets the guard's timings for a test and restores them after.
func guardNow(t *testing.T, cpuFor time.Duration) {
	t.Helper()
	oe, og, oc := procEvery, orphanGrace, agentCPUFor
	procEvery, orphanGrace, agentCPUFor = 0, 0, cpuFor
	t.Cleanup(func() { procEvery, orphanGrace, agentCPUFor = oe, og, oc })
}

func guardFleet(t *testing.T, cfg string) *Fleet {
	t.Helper()
	f, _ := parkFleet(t, cfg)
	b, _ := os.ReadFile(f.Home + "/tools.toml")
	os.WriteFile(f.Home+"/tools.toml", append(b, leaker...), 0o644)
	nf, err := New(f.T, f.Home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	return nf
}

func pidOf(t *testing.T, f *Fleet, name, key string) int {
	t.Helper()
	pane := waitPane(t, f, name)
	re := regexp.MustCompile(key + `=(\d+)`)
	for range 50 {
		out, _ := f.T.Capture(pane, 20)
		if m := re.FindStringSubmatch(out); m != nil {
			pid, _ := strconv.Atoi(m[1])
			t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
			return pid
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal(key + " pid never shown")
	return 0
}

func waitGone(pid int) bool {
	for i := 0; i < 50 && proc.Running(pid); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	return !proc.Running(pid)
}

func TestGuardReapsProcessesThatOutliveTheirAgent(t *testing.T) {
	guardNow(t, time.Hour)
	f := guardFleet(t, "")
	if _, err := f.Spawn("o1", "leak", f.Home, ""); err != nil {
		t.Fatal(err)
	}
	pid := pidOf(t, f, "o1", "orphan")
	daemon, user, parent := pidOf(t, f, "o1", "daemon"), pidOf(t, f, "o1", "user"), pidOf(t, f, "o1", "parent")
	// Same tag but another fleet home: not this fleet's to touch.
	other := exec.Command("sleep", "300")
	other.Env = append(os.Environ(), sysx.OwnerEnv+"="+OwnerTag(t.TempDir(), "o1"))
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Process.Kill(); other.Wait() })

	f.guardProcs(f.Store.Load().Sorted(), time.Now())
	if !proc.Running(pid) {
		t.Fatal("a live agent's out-of-tree helper was reaped")
	}
	if err := f.Close("o1"); err != nil {
		t.Fatal(err)
	}
	if !proc.Running(pid) {
		t.Fatal("the helper died with its pane; the test no longer leaves a leftover")
	}
	f.guardProcs(f.Store.Load().Sorted(), time.Now())
	if !waitGone(pid) || !waitGone(parent) {
		t.Fatalf("leftover processes %d, %d of a closed agent were not reaped", pid, parent)
	}
	if !proc.Running(daemon) {
		t.Error("a small daemon the agent started (a browser, an ssh master) was killed")
	}
	if !proc.Running(user) {
		t.Error("an untagged child of a leftover was killed")
	}
	if !proc.Running(other.Process.Pid) {
		t.Error("a process tagged for another fleet home was killed")
	}
	msgs, _ := f.Mail().Inbox(mail.Admiral, true, 10)
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1].Text, "leftover process") {
		t.Errorf("admiral not told about the reap: %+v", msgs)
	}
}

func TestGuardParksAgentOverMemoryCapEvenWhenWorking(t *testing.T) {
	guardNow(t, time.Hour)
	f := guardFleet(t, "[memory]\nagent_max = \"1KB\"\nagent_max_cpu = \"off\"\n")
	if _, err := f.Spawn("m1", "res", f.Home, ""); err != nil {
		t.Fatal(err)
	}
	waitPane(t, f, "m1")
	setState(t, f, "m1", "working", time.Now())
	f.guardProcs(f.Store.Load().Sorted(), time.Now())
	a := f.Store.Load().Agents["m1"]
	if a.Status != string(status.Parked) || f.paneOf("m1") != "" {
		t.Fatalf("over agent_max: status %s, pane %q; want parked and no pane", a.Status, f.paneOf("m1"))
	}
	if !strings.Contains(a.LastLine, "agent_max") || a.SessionID == "" {
		t.Errorf("parked line %q, session %q: want the reason and the session kept", a.LastLine, a.SessionID)
	}
}

func TestGuardParksIdleAgentBurningCPU(t *testing.T) {
	guardNow(t, 300*time.Millisecond)
	f := guardFleet(t, "[memory]\nagent_max = \"off\"\nagent_max_cpu = \"0.3\"\n")
	for _, n := range []string{"hot", "busy"} {
		if _, err := f.Spawn(n, "spin", f.Home, ""); err != nil {
			t.Fatal(err)
		}
		waitPane(t, f, n)
	}
	since := time.Now().Add(-time.Minute)
	setState(t, f, "hot", "idle", since)
	setState(t, f, "busy", "working", since)
	f.guardProcs(f.Store.Load().Sorted(), time.Now()) // first sample
	time.Sleep(600 * time.Millisecond)
	f.guardProcs(f.Store.Load().Sorted(), time.Now())
	b := f.Store.Load()
	if st := b.Agents["hot"].Status; st != string(status.Parked) {
		t.Errorf("idle agent burning a core: status %s, want parked", st)
	}
	if st := b.Agents["busy"].Status; st == string(status.Parked) {
		t.Error("a working agent was parked for its CPU")
	}
}
