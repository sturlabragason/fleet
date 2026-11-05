package fleet

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/supervisor"
)

// GitTimeout bounds each git call made for an agent's dir.
var GitTimeout = 3 * time.Second

// Git is what fleet knows about an agent's work tree.
type Git struct {
	Repo   bool   // inside a git work tree
	Top    string // work tree top level
	Branch string // current branch ("" when detached)
	Dirty  int    // lines of `git status --porcelain`; -1 if it failed
	Base   string // local main, or master when there is no main ("" = neither)
	Merged bool   // HEAD is an ancestor of Base
	Ahead  int    // commits on HEAD not on Base
}

// GitInfo reads dir's git state; each git call is bounded by GitTimeout.
func GitInfo(dir string) Git {
	git := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), GitTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
		return strings.TrimSpace(string(out)), err
	}
	var g Git
	if dir == "" { // git -C "" would read the caller's cwd
		return g
	}
	if out, err := git("rev-parse", "--is-inside-work-tree"); err != nil || out != "true" {
		return g
	}
	g.Repo = true
	g.Top, _ = git("rev-parse", "--show-toplevel")
	g.Branch, _ = git("symbolic-ref", "--quiet", "--short", "HEAD")
	if out, err := git("status", "--porcelain"); err != nil {
		g.Dirty = -1
	} else if out != "" {
		g.Dirty = len(strings.Split(out, "\n"))
	}
	for _, b := range []string{"main", "master"} {
		if _, err := git("rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil {
			g.Base = b
			break
		}
	}
	if g.Base == "" {
		return g
	}
	_, err := git("merge-base", "--is-ancestor", "HEAD", g.Base)
	g.Merged = err == nil
	if out, err := git("rev-list", "--count", g.Base+"..HEAD"); err == nil {
		g.Ahead, _ = strconv.Atoi(out)
	}
	return g
}

// CloseNote says what closing name could lose ("" = nothing known): its
// work tree has uncommitted changes or is not merged. An agent with its own
// worktree (DESIGN §4, ADR-39) has it named, with its branch: closing leaves
// both on disk.
func (f *Fleet) CloseNote(name string) string {
	a, ok := f.Store.Load().Agents[name]
	if !ok || a.Dir == "" {
		return ""
	}
	where := a.Dir
	if w := a.Worktree; w != nil {
		where = fmt.Sprintf("worktree %s, branch %s", w.Path, w.Branch)
	}
	switch r := CloseBlocker(a.Dir); r {
	case "", "not a git work tree", "no main or master branch":
		return ""
	case "uncommitted changes":
		return fmt.Sprintf("Note: %s has uncommitted changes in %s", name, where)
	default:
		return fmt.Sprintf("Note: %s's branch is %s (%s)", name, r, where)
	}
}

// modeRe reads Copilot's footer: " Autopilot · Allow All · ● Working …".
var modeRe = regexp.MustCompile(`(Autopilot|Interactive|Plan) · (Allow All|Manual Approval)`)

// Mode is the bottom-most mode footer in pane text ("" if none).
func Mode(text string) string {
	lines := status.Tail(text, status.Window)
	for i := len(lines) - 1; i >= 0; i-- {
		if m := modeRe.FindStringSubmatch(lines[i]); m != nil {
			return m[1] + " · " + m[2]
		}
	}
	return ""
}

// Info describes one agent as "key: value" lines: board facts, git state of
// its dir, session, context fill and the tool's mode.
func (f *Fleet) Info(name string) (string, error) {
	a, ok := f.Store.Load().Agents[name]
	if !ok {
		return "", fmt.Errorf("no agent %q", name)
	}
	now := time.Now()
	var b strings.Builder
	kv := func(k, v string) { fmt.Fprintf(&b, "%s: %s\n", k, v) }
	tm := ""
	if f.Teams != nil {
		tm, _ = f.Teams.Of(name)
	}
	kv("name", a.Name)
	kv("profile", a.Profile)
	kv("team", tm)
	kv("status", fmt.Sprintf("%s (for %s)", a.Status, now.Sub(a.StatusSince).Round(time.Second)))
	kv("last activity", now.Sub(a.LastChange).Round(time.Second).String()+" ago")
	kv("dir", a.Dir)
	if w := a.Worktree; w != nil {
		kv("agent worktree", w.Path+" on "+w.Branch)
	}
	if g := GitInfo(a.Dir); a.Dir == "" {
		kv("git", "no dir")
	} else if !g.Repo {
		kv("git", "not a git work tree")
	} else {
		kv("git worktree", g.Top)
		kv("git branch", g.Branch)
		if g.Dirty < 0 {
			kv("git dirty", "unknown (git status failed)")
		} else {
			kv("git dirty", strconv.Itoa(g.Dirty)+" files")
		}
		if g.Base == "" {
			kv("git merged", "no main or master branch")
		} else {
			kv("git merged into "+g.Base, map[bool]string{true: "yes", false: "no"}[g.Merged])
			kv("git ahead of "+g.Base, strconv.Itoa(g.Ahead))
		}
	}
	kv("session", a.SessionID)
	ctx := ""
	if f.Profiles[a.Profile].Patterns == "copilot" {
		if used, max, ok := supervisor.Fill(supervisor.CopilotHome(), a.SessionID); ok {
			ctx = strconv.Itoa(supervisor.Pct(used, max)) + "%"
		}
	}
	kv("context", ctx)
	mode := ""
	if p := f.paneOf(name); p != "" {
		if text, err := f.T.Capture(p, 0); err == nil {
			mode = Mode(text)
		}
	}
	kv("mode", mode)
	return strings.TrimRight(b.String(), "\n"), nil
}

// LastMessageMax caps LastMessage output (runes; the end is kept).
const LastMessageMax = 4000

// LastMessage is the agent's last main-agent reply from its tool's session
// event log (profile subagent_log).
func (f *Fleet) LastMessage(name string) (string, error) {
	a, ok := f.Store.Load().Agents[name]
	if !ok {
		return "", fmt.Errorf("no agent %q", name)
	}
	path := f.Profiles[a.Profile].SubagentLogPath(a.SessionID)
	if path == "" {
		return "", fmt.Errorf("last_message not available for profile %s", a.Profile)
	}
	msg, ok, err := status.LastMessage(path)
	if err != nil {
		return "", fmt.Errorf("last_message: %v", err)
	}
	if !ok {
		return "", fmt.Errorf("no assistant message yet")
	}
	if r := []rune(msg); len(r) > LastMessageMax {
		msg = "…" + string(r[len(r)-LastMessageMax+1:])
	}
	return msg, nil
}
