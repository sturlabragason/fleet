package fleet

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/worktree"
)

// GitTimeout bounds each git call made for an agent's dir.
var GitTimeout = 3 * time.Second

// Git is what fleet knows about an agent's work tree.
type Git struct {
	Repo   bool   // inside a git work tree
	Top    string // work tree top level
	Branch string // current branch ("" when detached)
	Dirty  int    // paths in `git status --porcelain` not ignored (untracked files one by one when ignoring); -1 if it failed
	Base   string // local main, or master when there is no main ("" = neither)
	Merged bool   // HEAD is an ancestor of Base
	Ahead  int    // commits on HEAD not on Base
}

// GitInfo reads dir's git state; each git call is bounded by GitTimeout.
// Dirty leaves out the absolute paths in ignore, such as the agent's own
// result file (#11).
func GitInfo(dir string, ignore ...string) Git {
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
	st := []string{"status", "--porcelain", "-z"}
	if len(ignore) > 0 { // list files one by one, so an ignored one isn't hidden in its dir
		st = append(st, "--untracked-files=all")
	}
	if out, err := gitOut(dir, st...); err != nil { // untrimmed: " M" starts with a space
		g.Dirty = -1
	} else {
		skip := worktree.Ignored(dir, ignore)
		for _, p := range statusPaths(out) {
			if !skip[p] {
				g.Dirty++
			}
		}
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

// statusPaths reads `git status --porcelain -z`: one path per entry, the new
// one for a rename or copy.
func statusPaths(out string) []string {
	var ps []string
	recs := strings.Split(out, "\x00")
	for i := 0; i < len(recs); i++ {
		r := recs[i]
		if len(r) < 4 {
			continue
		}
		ps = append(ps, r[3:])
		if r[0] == 'R' || r[0] == 'C' {
			i++ // -z puts the rename source in the next record
		}
	}
	return ps
}

// ownResult is the agent's task-card result file as an ignore list for
// GitInfo and CloseBlocker: writing it is part of the task, not work left
// behind (#11).
func ownResult(a *board.Agent) []string {
	if p, _, ok := ResultPath(a); ok {
		return []string{p}
	}
	return nil
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
	switch r := CloseBlocker(a.Dir, ownResult(a)...); r {
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
	if a.Leader {
		kv("squad", "leader of "+squadNames(f.Squad(name)))
	}
	if a.LeadBy != "" {
		kv("squad", "member of "+a.LeadBy+"'s squad; its reports go to "+a.LeadBy)
	}
	kv("status", fmt.Sprintf("%s (for %s)", a.Status, now.Sub(a.StatusSince).Round(time.Second)))
	switch {
	case a.Interactive:
		kv("interactive", "yes: the user talks to it directly; never auto-closed")
	case a.WithUser(now):
		kv("interactive", "the user typed in its pane "+now.Sub(a.UserAt).Round(time.Second).String()+" ago; not auto-closed for now")
	}
	kv("last activity", now.Sub(a.LastChange).Round(time.Second).String()+" ago")
	kv("dir", a.Dir)
	if c := a.Card; c != nil {
		if c.Goal != "" {
			kv("task goal", c.Goal)
		}
		if c.Check != "" {
			kv("done-check", fmt.Sprintf("%s (want exit %d)", c.Check, c.Want))
			kv("done-check result", CheckState(a))
		}
		if len(c.May) > 0 {
			kv("may", strings.Join(c.May, ", "))
		}
		if p, written, ok := ResultPath(a); ok {
			kv("result file", p+map[bool]string{true: " (written)", false: " (not written yet)"}[written])
		}
	}
	if w := a.Worktree; w != nil {
		kv("agent worktree", w.Path+" on "+w.Branch)
	}
	if g := GitInfo(a.Dir, ownResult(a)...); a.Dir == "" {
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
	if s, ok := f.SpendOf(a); ok {
		kv("spend", s.Long())
	}
	mode := ""
	if p := f.paneOf(name); p != "" && !a.ACP() {
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
