package proc

import (
	"os"
	"os/exec"
	"sort"
	"testing"
)

func TestOwnLeavesOutWorkBelowAShell(t *testing.T) {
	p := func(pid, ppid int, comm string) Info { return Info{Proc: Proc{PID: pid}, PPID: ppid, Comm: comm} }
	ps := []Info{
		p(10, 1, "bash"),     // the pane's shell: the root, so counted
		p(11, 10, "copilot"), // the agent
		p(12, 11, "node"),    // its helper
		p(13, 11, "bash"),    // a shell it ran
		p(14, 13, "go"),      // work under that shell
		p(15, 14, "compile"),
		p(20, 1, "other"),
	}
	var got []int
	for _, i := range Own(ps, 10) {
		got = append(got, i.PID)
	}
	sort.Ints(got)
	want := []int{10, 11, 12, 13}
	if len(got) != len(want) {
		t.Fatalf("Own = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Own = %v, want %v", got, want)
		}
	}
	if Own(ps, 99) != nil {
		t.Error("Own of a missing root is not nil")
	}
}

func TestScanFindsTaggedProcesses(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.Env = append(os.Environ(), "FLEET_TEST_OWNER=a@home")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	for _, i := range Scan("FLEET_TEST_OWNER") {
		if i.PID == cmd.Process.Pid {
			if i.Owner != "a@home" || i.PPID != os.Getpid() || i.Comm != "sleep" || i.RSS == 0 {
				t.Fatalf("scanned %+v", i)
			}
			return
		}
	}
	t.Fatal("tagged child not scanned")
}
