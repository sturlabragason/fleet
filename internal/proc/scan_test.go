package proc

import (
	"os"
	"os/exec"
	"sort"
	"testing"
	"time"
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
	// A child scanned in the instant after exec can still show RSS 0 (seen in CI), so retry briefly.
	var last *Info
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		for _, i := range Scan("FLEET_TEST_OWNER") {
			if i.PID == cmd.Process.Pid {
				i := i
				last = &i
			}
		}
		if last != nil && last.Comm == "sleep" && last.RSS != 0 {
			break
		}
	}
	if last == nil {
		t.Fatal("tagged child not scanned")
	}
	if last.Owner != "a@home" || last.PPID != os.Getpid() || last.Comm != "sleep" || last.RSS == 0 {
		t.Fatalf("scanned %+v", *last)
	}
}
