// Package tmux is a thin adapter over the tmux CLI, bound to one socket (-L).
package tmux

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
)

// Client runs tmux commands against one server socket.
type Client struct {
	Socket string // name passed to -L
	Config string // optional -f file (tests use /dev/null)
	// OnRun, when set, is told about every command before it runs (tests
	// count tmux execs with it).
	OnRun func(args []string)
	// Timeout bounds one command; 0 is DefaultTimeout. Commands that wait
	// on the user (Interactive) are never bounded.
	Timeout time.Duration
}

// DefaultTimeout bounds a tmux command. tmux answers in milliseconds; one
// that has not after this is hung (a wedged server, a stopped client), and
// without a bound it would stall the watcher's refresh, wakes and reaping
// for good (ADR-113).
const DefaultTimeout = 10 * time.Second

// Interactive reports whether a tmux command waits on the user: a popup,
// menu or prompt stays open until they close it.
func Interactive(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "display-popup", "popup", "display-menu", "menu", "confirm-before", "confirm", "command-prompt":
		return true
	}
	return false
}

func (c *Client) timeout(args []string) time.Duration {
	if Interactive(args) {
		return 0
	}
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultTimeout
}

// New returns a client for the given socket name.
func New(socket string) *Client { return &Client{Socket: socket} }

// base starts with -u: without a UTF-8 locale the client prints tabs in -F
// output as '_', which breaks every tab-separated parse (#21). -u, unlike
// LC_ALL=C.UTF-8, doesn't leak into the panes' environment.
func (c *Client) base() []string {
	a := []string{"-u", "-L", c.Socket}
	if c.Config != "" {
		a = append(a, "-f", c.Config)
	}
	return a
}

// Run executes tmux with args and returns trimmed stdout. Errors are logged.
func (c *Client) Run(args ...string) (string, error) {
	return c.RunInput("", args...)
}

// ErrTmpdirGone is returned instead of running tmux when TMUX_TMPDIR is set
// but no longer exists. tmux then silently falls back to /tmp/tmux-UID, so a
// leaked test process whose sandbox was cleaned up reached the user's real
// `fleet` server and unset its badge every poll, which blinked it (ADR-81).
var ErrTmpdirGone = fmt.Errorf("TMUX_TMPDIR is gone; refusing to fall back to the default tmux socket dir")

func tmpdirGone() bool {
	d := os.Getenv("TMUX_TMPDIR")
	if d == "" {
		return false
	}
	st, err := os.Stat(d)
	return err != nil || !st.IsDir()
}

// RunInput is Run with stdin.
func (c *Client) RunInput(stdin string, args ...string) (string, error) {
	if tmpdirGone() {
		return "", ErrTmpdirGone
	}
	if c.OnRun != nil {
		c.OnRun(args)
	}
	ctx := context.Background()
	if d := c.timeout(args); d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "tmux", append(c.base(), args...)...)
	cmd.WaitDelay = time.Second
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("tmux %s: timed out after %v", strings.Join(args, " "), c.timeout(args))
		logx.For("tmux").Error("command timed out", "socket", c.Socket, "err", err)
		return strings.TrimRight(out.String(), "\n"), err
	}
	if err != nil {
		err = fmt.Errorf("tmux %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
		if !quietErr(errb.String()) {
			logx.For("tmux").Warn("command failed", "socket", c.Socket, "err", err)
		}
	}
	return strings.TrimRight(out.String(), "\n"), err
}

func quietErr(s string) bool {
	return strings.Contains(s, "no server running") || strings.Contains(s, "error connecting")
}

// Running reports whether the server is up.
func (c *Client) Running() bool {
	_, err := c.Run("list-sessions")
	return err == nil
}

// HasSession reports whether a session exists.
func (c *Client) HasSession(name string) bool {
	_, err := c.Run("has-session", "-t", "="+name)
	return err == nil
}

// KillServer stops this socket's server. Throwaway test sockets are removed too.
func (c *Client) KillServer() error {
	path := c.SocketPath()
	if path == "" {
		dir := os.Getenv("TMUX_TMPDIR")
		if dir == "" {
			dir = "/tmp"
		}
		path = fmt.Sprintf("%s/tmux-%d/%s", dir, os.Getuid(), c.Socket)
	}
	_, err := c.Run("kill-server")
	if strings.HasPrefix(c.Socket, "fleet-test-") && strings.HasSuffix(path, "/"+c.Socket) {
		os.Remove(path)
	}
	return err
}

// SocketPath returns the server socket path.
func (c *Client) SocketPath() string {
	s, _ := c.Run("display-message", "-p", "#{socket_path}")
	return s
}
