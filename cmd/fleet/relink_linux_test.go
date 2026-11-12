package main

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/tmux"
)

func TestStaleLinkClients(t *testing.T) {
	listing := strings.Join([]string{
		"/dev/pts/1\t101\txterm-256color\tclipboard,sync,title\tfleet\t0", // stale
		"/dev/pts/2\t102\txterm-256color\tclipboard,hyperlinks\tfleet\t0", // has it
		"/dev/pts/3\t103\tscreen-256color\tclipboard\tfleet\t0",           // not xterm*
		"client-4\t104\txterm\t\tfleet\t1",                                // control mode
		"/dev/pts/5\t105\txterm-kitty\tRGB\tfleet-wall\t0",                // stale
		"/dev/pts/6\t106\txterm\tRGB\tfleet\t0",                           // done before
	}, "\n")
	got := staleLinkClients(listing, "999 106")
	want := []staleClient{{"/dev/pts/1", "101", "fleet"}, {"/dev/pts/5", "105", "fleet-wall"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// The regression behind a wrapped URL opening cut off in the admiral pane:
// a client attached before fleet set terminal-features[91] never gets the
// hyperlinks feature, so tmux sends OSC 8 links as plain text. relinkClients
// must re-attach it in place (same process) with the feature, and only once.
func TestRelinkClientsGivesOldClientHyperlinks(t *testing.T) {
	c := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { c.KillServer() })
	if _, err := c.Run("new-session", "-d", "-s", fleet.Session, "-x", "80", "-y", "24", "sleep 600"); err != nil {
		t.Skip("tmux unavailable")
	}
	m, slave, err := openPTY()
	if err != nil {
		t.Skip("no pty: ", err)
	}
	defer m.Close()
	cl := exec.Command("tmux", "-L", c.Socket, "-f", "/dev/null", "attach", "-t", fleet.Session)
	cl.Stdin, cl.Stdout, cl.Stderr = slave, slave, slave
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "TMUX=") && !strings.HasPrefix(e, "TERM=") {
			env = append(env, e)
		}
	}
	cl.Env = append(env, "TERM=xterm-256color", "SHELL=/bin/sh")
	cl.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cl.Start(); err != nil {
		slave.Close()
		t.Skip("cannot attach: ", err)
	}
	slave.Close()
	defer func() { cl.Process.Kill(); cl.Wait() }()
	go func() {
		b := make([]byte, 4096)
		for {
			if _, err := m.Read(b); err != nil {
				return
			}
		}
	}()
	clients := func() string {
		out, _ := c.Run("list-clients", "-F", "#{client_pid} #{client_termfeatures}")
		return strings.TrimSpace(out)
	}
	waitFor := func(what string, ok func(string) bool) string {
		t.Helper()
		var s string
		for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
			if s = clients(); ok(s) {
				return s
			}
		}
		t.Fatalf("timed out waiting for %s; clients: %q", what, s)
		return ""
	}
	before := waitFor("the client", func(s string) bool { return s != "" })
	if strings.Contains(before, "hyperlinks") {
		t.Fatalf("client has hyperlinks before the option: %q", before)
	}
	c.Run("set-option", "-s", "terminal-features[91]", hyperlinkTerms+":hyperlinks")
	f, err := fleet.New(c, t.TempDir(), "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if n := relinkClients(f); n != 1 {
		t.Fatalf("relinked %d clients, want 1", n)
	}
	after := waitFor("the re-attach", func(s string) bool { return strings.Contains(s, "hyperlinks") })
	if pid := fmt.Sprint(cl.Process.Pid); !strings.HasPrefix(after, pid+" ") {
		t.Fatalf("re-attached client %q is not the terminal's process %s", after, pid)
	}
	if n := relinkClients(f); n != 0 {
		t.Fatalf("second pass relinked %d clients", n)
	}
}
