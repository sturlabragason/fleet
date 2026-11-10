package fleet

import (
	"path/filepath"
	"sort"
	"time"

	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/status"
)

// WatchPaths are the files whose mtime means an agent did something, for the
// watcher's event check (DESIGN §7c, ADR-49):
//
//   - ~/.fleet/status, the directory the tools' own hooks write into. One
//     stat covers every hook-driven agent, whatever its name.
//   - ~/.fleet/status/subs, where the hooks keep subagents (ADR-99).
//   - each agent's session event log (copilot's events.jsonl, Claude's
//     session jsonl): the tool appends to it as it works, so its size moves
//     on every turn, tool call and subagent.
//
// Resolved session globs are cached: the directory a tool files a session
// under does not change while the agent runs.
func (f *Fleet) WatchPaths() []string {
	out := []string{status.StateDir(f.Home), status.SubsDir(f.Home)}
	seen := map[string]bool{}
	for _, a := range f.Store.Load().Agents {
		for _, p := range f.sessionLogs(a) {
			if p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out[2:])
	return out
}

// sessionLogs are the on-disk logs of one agent's session.
func (f *Fleet) sessionLogs(a *board.Agent) []string {
	if a.SessionID == "" || pending(a.Status) {
		return nil
	}
	p := f.Profiles[a.Profile]
	var out []string
	if l := p.SubagentLogPath(a.SessionID); l != "" {
		out = append(out, l)
	} else if a.ACP() {
		out = append(out, acphost.TranscriptPath(f.Home, a.Name))
	}
	if g := p.SessionGlob(a.SessionID); g != "" {
		out = append(out, f.resolveGlob(a.Name, g))
	}
	return out
}

// resolveGlob expands a session glob once per agent and remembers the answer;
// an unmatched glob is retried next time, since the file appears a moment
// after the tool starts.
func (f *Fleet) resolveGlob(agent, g string) string {
	f.globMu.Lock()
	defer f.globMu.Unlock()
	if p, ok := f.globs[agent]; ok && p != "" {
		return p
	}
	m, err := filepath.Glob(g)
	if err != nil || len(m) == 0 {
		return ""
	}
	sort.Strings(m)
	if f.globs == nil {
		f.globs = map[string]string{}
	}
	f.globs[agent] = m[0]
	return m[0]
}

// hookState is the last thing an agent's own hooks said about it
// (~/.fleet/status/<name>.json, DESIGN §4). ok=false when there is none, or
// when the screen is more recent evidence: a hook only overrides the pane
// when the pane said nothing fleet recognises, or when the hook fired after
// the pane last changed.
func (f *Fleet) hookState(a *board.Agent, screenOK bool) (status.State, bool) {
	s, ok := status.ReadState(status.StateDir(f.Home), a.Name)
	if !ok {
		return status.State{}, false
	}
	if screenOK && !s.At.After(a.LastChange) {
		return status.State{}, false
	}
	// "Working" is the one hook state that goes stale on its own: idle and
	// waiting are where an agent rests, but a tool killed mid-tool-call
	// leaves its last PreToolUse behind and would stay working forever.
	if s.Status == status.Working && time.Since(s.At) > HookWorkingMax {
		return status.State{}, false
	}
	return s, true
}

// HookWorkingMax is how long a hook may hold an agent at "working" with no
// newer evidence; after that the screen decides again (ADR-49).
const HookWorkingMax = 2 * time.Minute

// applyHook lets a hook decide the status and, for a prompt, the line shown
// as waiting_for.
func applyHook(a *board.Agent, s status.State) status.Status {
	if s.WaitingFor != "" {
		a.LastLine = status.Cut(s.WaitingFor, 200)
	}
	logx.For("status").Debug("hook state", "agent", a.Name, "event", s.Event, "status", s.Status)
	return s.Status
}

// forgetHook drops an agent's hook state, so a new agent of the same name
// never inherits it.
func (f *Fleet) forgetHook(name string) {
	status.DropState(status.StateDir(f.Home), name)
	status.DropHookSubs(f.Home, name)
	status.DropStopBlock(f.Home, name)
}
