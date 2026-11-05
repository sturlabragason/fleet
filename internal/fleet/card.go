package fleet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
)

// Task cards (DESIGN §18 #5/#6, ADR-43). A spawn may attach a goal, a
// done-check (command + expected exit code) and a result file. The card is
// board data, the agent is told about it in its first prompt, and fleet runs
// the done-check itself when the agent reports done.

// DefaultResult is the result file a carded agent is told to write, relative
// to its dir (DESIGN §18 #6).
const DefaultResult = "result.md"

// CheckTimeout bounds one done-check run. A check that outlives it fails
// with "timed out", so a hanging command never holds an agent open.
var CheckTimeout = 2 * time.Minute

// checkWaitDelay is how long a killed check may take to let go of its
// output pipe before Run gives up on it.
const checkWaitDelay = 2 * time.Second

// MaxCheckOut caps the done-check output kept on the board, in runes.
const MaxCheckOut = 2000

// NormalizeCard trims a card and fills its defaults; it returns nil when
// nothing was asked for, so "no card" stays one value everywhere. Checked is
// always cleared: a fresh spawn has not been checked.
func NormalizeCard(c *board.Card) *board.Card {
	if c == nil {
		return nil
	}
	n := board.Card{
		Goal:   strings.TrimSpace(c.Goal),
		Check:  strings.TrimSpace(c.Check),
		Want:   c.Want,
		Result: strings.TrimSpace(c.Result),
	}
	if n.Goal == "" && n.Check == "" && n.Result == "" {
		return nil
	}
	if n.Result == "" {
		n.Result = DefaultResult
	}
	return &n
}

// CardPrompt is the block appended to a carded agent's first prompt, so the
// agent knows the goal, the check fleet will run and where to write its
// result. "" when there is no card.
func CardPrompt(c *board.Card) string {
	if c == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Task card\n")
	if c.Goal != "" {
		fmt.Fprintf(&b, "\nGoal: %s\n", c.Goal)
	}
	if c.Check != "" {
		fmt.Fprintf(&b, "\nDone-check: `%s`, run in your dir, must exit %d. fleet runs it itself when you report done and tells the admiral whether it passed, so run it yourself first.\n", c.Check, c.Want)
	}
	fmt.Fprintf(&b, "\nResult file: write `%s` in your dir before you report done: what you changed, the branch and commit, what is left and any open questions.\n", c.Result)
	return strings.TrimRight(b.String(), "\n")
}

// RunCheck runs a card's done-check in dir and reports the verdict. It never
// returns nil: a command that cannot start or does not finish is a failure
// with a reason.
func RunCheck(dir string, c *board.Card, timeout time.Duration) *board.Check {
	res := &board.Check{At: time.Now(), Cmd: c.Check, Want: c.Want, Code: -1}
	if c.Check == "" {
		res.Err = "no done-check"
		return res
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", c.Check)
	cmd.Dir = dir
	// Its own process group, killed as a group: a check is usually a build
	// or a test run, and its children must not outlive the timeout or keep
	// the output pipe open past it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = checkWaitDelay
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	res.Out = tailRunes(buf.String(), MaxCheckOut)
	var ee *exec.ExitError
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, exec.ErrWaitDelay):
		res.Err = "timed out after " + timeout.String()
	case err == nil:
		res.Code = 0
	case errors.As(err, &ee):
		res.Code = ee.ExitCode()
	default:
		res.Err = err.Error()
	}
	res.Pass = res.Err == "" && res.Code == res.Want
	return res
}

// tailRunes keeps the last n runes of s, marking what it cut.
func tailRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return "…" + string(r[len(r)-n+1:])
	}
	return s
}

// ResultPath is the absolute path of an agent's result file, and whether it
// exists. ok is false when the agent has no card.
func ResultPath(a *board.Agent) (path string, written, ok bool) {
	if a == nil || a.Card == nil || a.Card.Result == "" {
		return "", false, false
	}
	path = a.Card.Result
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.Dir, path)
	}
	st, err := os.Stat(path)
	return path, err == nil && !st.IsDir(), true
}

// CheckState is how an agent's done-check stands, in one line: its verdict
// once it has run, "running" while fleet runs it after a done report, else
// "not run yet". "" when the agent has no done-check.
func CheckState(a *board.Agent) string {
	if a == nil || a.Card == nil || a.Card.Check == "" {
		return ""
	}
	if ch := a.Card.Checked; ch != nil {
		return ch.Verdict() + " at " + ch.At.Format("2006-01-02 15:04:05")
	}
	if r := a.Report; r != nil && r.State == "done" {
		return "running"
	}
	return "not run yet"
}
