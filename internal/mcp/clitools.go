package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
)

// CLI-backed admiral tools (ADR-86): fleet commands whose work lives in the
// fleet binary's own command code (wall placement, git walks, admiral panes).
// The tool runs that command, so the MCP tool and the CLI can't drift apart.

var cliToolDefs = []tool{
	{"fleet_takeover", "Move an idle acp agent's session into the tool's own screen (tmux), in the same wall slot; acp=true hands a tmux agent back to fleet's acp host. Use when the user wants the tool's real UI for an agent.", obj([]string{"name"}, map[string]any{
		"name": str("agent name"), "acp": map[string]any{"type": "boolean", "description": "move it back from tmux to the acp host"}})},
	{"fleet_touching", "Files other worktrees of a repo change (merge order: fewer touched files merges first). With files, only the overlaps.", obj([]string{"dir"}, map[string]any{
		"dir": str("a directory inside the repo (absolute)"), "files": strList("files to check for overlap, relative to dir")})},
	{"fleet_worktrees", "A repo's leftover agent worktrees under <repo>.wt/: size, merged, dirty, the agent holding each. prune=true removes the merged, clean ones no live or parked agent holds (git refuses uncommitted work; never forced): ask the user first, confirmed=true only after an explicit yes.", obj([]string{"dir"}, map[string]any{
		"dir": str("the repo (absolute)"), "prune": map[string]any{"type": "boolean", "description": "remove the prunable worktrees"}, "confirmed": confirmedProp})},
	{"fleet_branches", "A repo's fleet/ branches on origin, merged or not, and the agent on each. prune=true deletes the merged ones no live or parked agent is on, from origin: ask the user first, confirmed=true only after an explicit yes.", obj([]string{"dir"}, map[string]any{
		"dir": str("the repo (absolute)"), "prune": map[string]any{"type": "boolean", "description": "delete the merged branches from origin"}, "confirmed": confirmedProp})},
	{"fleet_admiral_add", "Start another admiral beside main (its own pane, agents and reports), only when the user asks. fleet_admirals lists them.", obj([]string{"name"}, map[string]any{
		"name": str("new admiral name"), "profile": str("tool profile (default: main's), see fleet_profiles")})},
	{"fleet_admiral_close", "Close another admiral (main only, only when the user asks): its pane closes and its agents and unread reports come to main.", obj([]string{"name"}, map[string]any{
		"name": str("admiral name, see fleet_admirals")})},
	{"fleet_changes", "What's new in fleet: the running version and its latest changelog lines, newest first. Use when the user asks what changed, or after [fleet updated].", obj(nil, map[string]any{
		"limit": map[string]any{"type": "integer", "description": "lines to return (default 15)"}})},
}

func init() { toolDefs = append(toolDefs, cliToolDefs...) }

// cliTimeout bounds one CLI-backed call; a git walk over many worktrees or a
// push to origin may take a while, but never block the MCP loop for good.
const cliTimeout = 2 * time.Minute

// runFleet runs `<fleet binary> args...` in dir and returns its combined
// output and exit code; err is set only when it could not run or timed out.
func (s *Server) runFleet(dir string, args ...string) (string, int, error) {
	if s.RunCLI != nil {
		return s.RunCLI(dir, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cliTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.F.Exe, args...)
	cmd.Dir = dir
	// git children inherit the output pipe; don't wait on them past the kill.
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ctx.Err() == nil {
		return out.String(), ee.ExitCode(), nil
	}
	if err != nil {
		return out.String(), -1, fmt.Errorf("fleet %s: %v", args[0], err)
	}
	return out.String(), 0, nil
}

// cliResult turns a run into a tool result: a non-zero exit is an error
// carrying the command's own message.
func cliResult(out string, code int, err error) (string, error) {
	out = strings.TrimSpace(out)
	if err != nil {
		return out, err
	}
	if code != 0 {
		if out == "" {
			out = fmt.Sprintf("exit %d", code)
		}
		return "", errors.New(out)
	}
	return out, nil
}

func absDir(dir string) error {
	if dir == "" || !filepath.IsAbs(dir) {
		return fmt.Errorf("dir must be an absolute path")
	}
	return nil
}

// cliCall runs the tools of cliToolDefs.
func (s *Server) cliCall(name string, a args) (string, error) {
	switch name {
	case "fleet_takeover":
		if a.Name == "" {
			return "", fmt.Errorf("name is required")
		}
		if err := s.scope(a.Name); err != nil {
			return "", err
		}
		argv := []string{"takeover", a.Name}
		if a.ACP {
			argv = append(argv, "--acp")
		}
		return cliResult(s.runFleet("", argv...))
	case "fleet_touching":
		if err := absDir(a.Dir); err != nil {
			return "", err
		}
		out, code, err := s.runFleet(a.Dir, append([]string{"touching", "-C", a.Dir}, a.Files...)...)
		if err == nil && code == 1 { // 1 = overlap, an answer rather than a failure
			return "overlap:\n" + strings.TrimSpace(out), nil
		}
		if out, err = cliResult(out, code, err); err == nil && out == "" {
			out = "no other worktree changes files"
		}
		return out, err
	case "fleet_worktrees", "fleet_branches":
		if err := absDir(a.Dir); err != nil {
			return "", err
		}
		argv := []string{strings.TrimPrefix(name, "fleet_"), "--repo", a.Dir}
		if a.Prune {
			if !a.Confirmed {
				return "", fmt.Errorf("prune needs confirmed=true after the user said yes; list first without prune and ask")
			}
			argv = append(argv, "--prune")
		}
		return cliResult(s.runFleet(a.Dir, argv...))
	case "fleet_admiral_add":
		if strings.TrimSpace(a.Name) == "" {
			return "", fmt.Errorf("name is required")
		}
		argv := []string{"admiral", "add", a.Name}
		if a.Profile != "" {
			argv = append(argv, "--profile", a.Profile)
		}
		return cliResult(s.runFleet("", argv...))
	case "fleet_admiral_close":
		if strings.TrimSpace(a.Name) == "" {
			return "", fmt.Errorf("name is required")
		}
		if s.Me() != board.Main {
			return "", fmt.Errorf("only main closes an admiral; ask main or the user")
		}
		return cliResult(s.runFleet("", "admiral", "close", a.Name))
	}
	// fleet_changes
	out, err := cliResult(s.runFleet("", "changes"))
	if err != nil {
		return "", err
	}
	n := a.Limit
	if n <= 0 {
		n = 15
	}
	if lines := strings.Split(out, "\n"); len(lines) > n {
		out = strings.Join(lines[:n], "\n") + fmt.Sprintf("\n(%d more lines: limit=N for more)", len(lines)-n)
	}
	return out, nil
}
