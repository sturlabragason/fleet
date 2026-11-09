package proc

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestKillStopsWholeTree(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 60 & sleep 60 & wait")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	var ps []Proc
	for i := 0; i < 50 && len(ps) < 3; i++ {
		time.Sleep(20 * time.Millisecond)
		ps = Tree(cmd.Process.Pid)
	}
	if len(ps) < 3 || ps[0].PID != cmd.Process.Pid {
		t.Fatalf("tree: %+v", ps)
	}
	Kill(ps, time.Second)
	cmd.Wait()
	for _, p := range ps {
		if alive(p) {
			t.Errorf("pid %d still running", p.PID)
		}
	}
}

func TestKillEscalatesToSIGKILL(t *testing.T) {
	cmd := exec.Command("sh", "-c", "trap '' TERM; sleep 60 & wait; wait")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	time.Sleep(100 * time.Millisecond)
	ps := Tree(cmd.Process.Pid)
	Kill(ps, 200*time.Millisecond)
	cmd.Wait()
	for _, p := range ps {
		if alive(p) {
			t.Errorf("pid %d survived SIGKILL", p.PID)
		}
	}
}

func TestKillSparesSelfAndReusedPids(t *testing.T) {
	self := Tree(os.Getpid())[0]
	Kill([]Proc{self, {PID: 1}, {PID: self.PID, Start: self.Start + 1}}, 10*time.Millisecond)
	if Tree(0) != nil || Tree(-1) != nil {
		t.Error("tree of pid <= 0")
	}
}
