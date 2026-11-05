package main

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// ADR-38's claim is about behaviour, not about binding strings: a left-button
// drag over a pane whose program asked for the mouse must run copy-command.
// The test drives a real SGR mouse drag through an attached client, over a
// pane that turned mouse reporting on, with copy-command writing to a file.
// tmux's own defaults fail it; mouseSelectBindings passes it.
func TestMouseDragOverMouseReportingPaneCopies(t *testing.T) {
	for _, c := range []struct {
		name  string
		bind  bool
		role  string
		wantC bool
	}{
		{"tmux defaults copy nothing", false, "", false},
		{"fleet bindings copy the drag", true, "", true},
		// The overview's cards answer the click, so it keeps the old
		// behaviour and a drag there must not take the clipboard (ADR-31).
		{"the overview keeps its clicks", true, "ov", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := dragCopy(t, c.bind, c.role)
			if c.wantC && !strings.Contains(got, "ALPHA BRAVO") {
				t.Errorf("clipboard %q, want the dragged text", got)
			}
			if !c.wantC && got != "" {
				t.Errorf("clipboard %q, want nothing copied", got)
			}
		})
	}
}

// dragCopy starts a tmux server whose pane runs a program with mouse
// reporting on, optionally applies mouseSelectBindings, drags the left button
// across the first row and returns whatever copy-command received.
func dragCopy(t *testing.T, bind bool, role string) string {
	t.Helper()
	clip := filepath.Join(t.TempDir(), "clip")
	c := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { c.KillServer() })
	if _, err := c.Run("new-session", "-d", "-s", "f", "-x", "80", "-y", "24", "sh"); err != nil {
		t.Skip("tmux unavailable")
	}
	c.Run("set-option", "-g", "mouse", "on")
	if role != "" {
		pane, _ := c.Run("display-message", "-p", "-t", "f", "#{pane_id}")
		c.SetPaneOption(strings.TrimSpace(pane), "@role", role)
	}
	c.Run("set-option", "-s", "copy-command", "cat > "+clip)
	if bind {
		for _, a := range tmuxCommands(&fleet.Fleet{Exe: "/bin/true"}) {
			if len(a) == 4 && a[0] == "bind-key" && isMouseSelectKey(a[2]) {
				if _, err := c.Run(a...); err != nil {
					t.Fatalf("tmux rejected %v: %v", a, err)
				}
			}
		}
	}
	// 1000/1002/1006: the pane's program takes the mouse, like Copilot does.
	c.Run("send-keys", "-t", "f", `clear; printf 'ALPHA BRAVO CHARLIE DELTA\n';`+
		` stty -echo; printf '\033[?1000h\033[?1002h\033[?1006h'; cat >/dev/null`, "Enter")
	time.Sleep(700 * time.Millisecond)
	if v, _ := c.Run("display-message", "-p", "-t", "f", "#{mouse_any_flag}"); strings.TrimSpace(v) != "1" {
		t.Skip("the pane did not take the mouse; nothing to test")
	}

	m, slave, err := openPTY()
	if err != nil {
		t.Skip("no pty: ", err)
	}
	defer m.Close()
	cl := exec.Command("tmux", "-L", c.Socket, "-f", "/dev/null", "attach", "-t", "f")
	cl.Stdin, cl.Stdout, cl.Stderr = slave, slave, slave
	cl.Env = append(os.Environ(), "TERM=xterm-256color")
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
	time.Sleep(900 * time.Millisecond)
	// Press at column 1, move right twice, release: a drag across row 1.
	for _, seq := range []string{"\033[<0;1;1M", "\033[<32;10;1M", "\033[<32;24;1M", "\033[<0;24;1m"} {
		m.WriteString(seq)
		time.Sleep(350 * time.Millisecond)
	}
	time.Sleep(900 * time.Millisecond)
	b, _ := os.ReadFile(clip)
	return string(b)
}

func isMouseSelectKey(k string) bool {
	for _, s := range mouseSelectKeys {
		if s == k {
			return true
		}
	}
	return false
}

// openPTY opens a pty master and its unlocked slave, so tmux has a terminal
// to attach to. Linux ioctls, hence the build constraint in the file name;
// the test skips wherever they are unavailable.
func openPTY() (*os.File, *os.File, error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock int32
	var n uint32
	if err := ioctl(m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); err != nil {
		m.Close()
		return nil, nil, err
	}
	if err := ioctl(m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); err != nil {
		m.Close()
		return nil, nil, err
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	return m, s, nil
}

func ioctl(fd, req, arg uintptr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg); e != 0 {
		return e
	}
	return nil
}
