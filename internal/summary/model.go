package summary

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// waitDelay is how long Run waits for a killed CLI's children to let go of
// its output pipes before it gives up on them. Without it a CLI that leaves a
// grandchild holding stdout (node launchers do) makes Run block past every
// timeout, and with it one wedged summary would wedge the MCP server.
const waitDelay = 2 * time.Second

// DefaultModel is the summariser model (ADR-40): Haiku 5.5, reached through
// the Copilot CLI that fleet already uses as a tool profile, so there is no
// API key anywhere and no local model competing for this machine's RAM.
const DefaultModel = "claude-haiku-5.5"

// DefaultCommand is the CLI the summariser shells out to.
const DefaultCommand = "copilot"

// DefaultTimeout bounds one model call. A summary that is late is useless:
// peek returns raw lines instead.
const DefaultTimeout = 45 * time.Second

// DefaultArgs runs one non-interactive prompt and prints only the answer.
// {prompt} and {model} are replaced; a profile without {prompt} gets the
// prompt on stdin instead. The lean flags are what make a summary cheap:
// without them the CLI's own instructions and tool definitions cost about
// 36k input tokens per call, with them about 6k (measured 2026-11-05).
func DefaultArgs() []string {
	return []string{"-p", "{prompt}", "--model", "{model}", "--silent",
		"--no-custom-instructions", "--available-tools", "none",
		"--disable-builtin-mcps", "--log-level", "none", "--no-color"}
}

// CLI is a Model backed by an installed agent CLI in non-interactive mode.
// It inherits that CLI's own authentication, so fleet never holds a key.
type CLI struct {
	Command string        // "" = DefaultCommand
	Args    []string      // nil = DefaultArgs()
	Model   string        // "" = DefaultModel
	Timeout time.Duration // 0 = DefaultTimeout
	Dir     string        // working directory; "" = a temp dir, so the CLI
	// does not load the agent repo's instructions into a summary call
}

// Name is the model the CLI was asked for (Summarizer records it).
func (c *CLI) Name() string {
	if c.Model != "" {
		return c.Model
	}
	return DefaultModel
}

// Summarize runs the CLI once and returns its stdout.
func (c *CLI) Summarize(ctx context.Context, prompt string) (string, error) {
	cmd := c.Command
	if cmd == "" {
		cmd = DefaultCommand
	}
	if _, err := exec.LookPath(cmd); err != nil {
		return "", fmt.Errorf("summary: %s not installed", cmd)
	}
	args := c.Args
	if args == nil {
		args = DefaultArgs()
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	onStdin := true
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.Contains(a, "{prompt}") {
			onStdin = false
			a = strings.ReplaceAll(a, "{prompt}", prompt)
		}
		out = append(out, strings.ReplaceAll(a, "{model}", c.Name()))
	}

	ex := exec.CommandContext(ctx, cmd, out...)
	// Its own process group, killed as a group: the CLI's children must not
	// outlive the call or keep its pipes open.
	ex.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	ex.Cancel = func() error { return syscall.Kill(-ex.Process.Pid, syscall.SIGKILL) }
	ex.WaitDelay = waitDelay
	ex.Dir = c.Dir
	if ex.Dir == "" {
		ex.Dir = os.TempDir()
	}
	ex.Env = append(os.Environ(), "NO_COLOR=1")
	if onStdin {
		ex.Stdin = strings.NewReader(prompt)
	}
	var stdout, stderr bytes.Buffer
	ex.Stdout, ex.Stderr = &stdout, &stderr
	if err := ex.Run(); err != nil {
		if ctx.Err() != nil || errors.Is(err, exec.ErrWaitDelay) {
			return "", fmt.Errorf("summary: %s timed out after %s", cmd, timeout)
		}
		return "", fmt.Errorf("summary: %s: %v: %s", cmd, err, firstLine(stderr.String()))
	}
	return stdout.String(), nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
