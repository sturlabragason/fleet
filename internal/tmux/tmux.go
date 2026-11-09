// Package tmux is a thin adapter over the tmux CLI, bound to one socket (-L).
package tmux

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/sturlabragason/fleet/internal/logx"
)

// Client runs tmux commands against one server socket.
type Client struct {
	Socket string // name passed to -L
	Config string // optional -f file (tests use /dev/null)
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

// RunInput is Run with stdin.
func (c *Client) RunInput(stdin string, args ...string) (string, error) {
	cmd := exec.Command("tmux", append(c.base(), args...)...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
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
