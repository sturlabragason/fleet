// Package tools holds data-driven tool profiles (built-in defaults merged with
// ~/.fleet/tools.toml) and builds the spawn/resume command lines.
package tools

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sturlabragason/fleet/internal/conf"
	"github.com/sturlabragason/fleet/internal/logx"
)

// Profile describes how to launch one tool variant.
type Profile struct {
	Name     string
	Command  string
	Args     []string
	Env      map[string]string
	LaunchID []string // args to set a session id at launch, with {id}; empty = not supported
	Resume   []string // args to resume, with {id}
	Patterns string   // status pattern set name
	// SessionPath is a glob (with {id} and a leading ~) where the tool stores a
	// session; used by adopt to check it exists. Empty = no check.
	SessionPath string
	// SubagentLog is the tool's session event log (with {id} and a leading ~),
	// read for subagent dots (DESIGN.md §4 Subagents). Empty = no dots.
	SubagentLog string

	// PromptArgs carry the initial prompt at launch, with {prompt} substituted.
	// When set and a prompt is given, fleet does not paste afterwards.
	PromptArgs []string
	// ModeArgs maps a starting-mode name to the args that select it.
	ModeArgs map[string][]string
	// ModeKeys maps a starting-mode name to tmux key names sent once the pane
	// is ready, for tools that only offer a keystroke.
	ModeKeys map[string][]string
	// Ready is a regex that means "the TUI is accepting input". Empty = fall
	// back to the profile's status patterns.
	Ready string
	// PeerArgs wire the agent-scoped fleet MCP server (DESIGN §6.6), with
	// {peer_mcp} / {peer_mcp_claude}. Inserted straight after the command.
	PeerArgs []string
	// HookArgs wire the tool's own status hooks to `fleet hook`, with
	// {hooks} (ADR-49). They are per-agent settings passed on the command
	// line, so the user's own settings file is never written to. Left out
	// when the vars carry no hook config.
	HookArgs []string
}

// HasMode reports whether the profile knows the named starting mode.
func (p Profile) HasMode(mode string) bool {
	if mode == "" {
		return true
	}
	_, a := p.ModeArgs[mode]
	_, k := p.ModeKeys[mode]
	return a || k
}

// Defaults are the built-in profiles. {fleet} is the fleet executable, {name}
// the agent name, {peer_mcp}/{peer_mcp_claude} the agent's peer MCP config and
// {hooks} its status-hook settings.
const Defaults = `
[copilot]
command = "copilot"
prompt_args = ["-i", "{prompt}"]
mode_args = { autopilot = "--allow-all --autopilot", plan = "--plan", interactive = "--mode interactive" }
launch_id = ["--session-id", "{id}"]
resume = ["--resume", "{id}"]
session_path = "~/.copilot/session-state/{id}"
subagent_log = "~/.copilot/session-state/{id}/events.jsonl"
patterns = "copilot"
peer_args = ["--additional-mcp-config", "{peer_mcp}"]

[claude-copilot]
command = "copilot"
args = ["--model", "claude-opus-5", "--allow-all"]
prompt_args = ["-i", "{prompt}"]
mode_args = { autopilot = "--allow-all --autopilot", plan = "--plan", interactive = "--mode interactive" }
launch_id = ["--session-id", "{id}"]
resume = ["--resume", "{id}"]
session_path = "~/.copilot/session-state/{id}"
subagent_log = "~/.copilot/session-state/{id}/events.jsonl"
patterns = "copilot"
peer_args = ["--additional-mcp-config", "{peer_mcp}"]

[claude]
command = "claude"
prompt_args = ["{prompt}"]
mode_args = { accept-edits = "--permission-mode acceptEdits", plan = "--permission-mode plan", bypass = "--permission-mode bypassPermissions" }
launch_id = ["--session-id", "{id}"]
resume = ["--resume", "{id}"]
session_path = "~/.claude/projects/*/{id}.jsonl"
peer_args = ["--mcp-config", "{peer_mcp_claude}"]
hook_args = ["--settings", "{hooks}"]
patterns = "claude"

[agy]
command = "agy"
resume = ["--conversation", "{id}"]
patterns = "agy"

[vibe]
command = "vibe"
resume = ["--resume", "{id}"]
patterns = "vibe"

[fake]
command = "{fleet}"
args = ["fakeagent", "{name}"]
patterns = "fake"
ready = "fakeagent .+ ready"
`

// Path is the user override file.
func Path(home string) string { return filepath.Join(home, "tools.toml") }

// Load returns built-in profiles overridden field-by-field by the user file.
func Load(home string) (map[string]Profile, error) { return LoadWith(home, nil) }

// LoadWith is Load plus a third override layer (the [profile.<name>] sections
// of the local preferences file, already re-keyed to profile names).
func LoadWith(home string, local conf.Doc) (map[string]Profile, error) {
	def, err := conf.Parse(Defaults)
	if err != nil {
		return nil, err
	}
	user, err := conf.ParseFile(Path(home))
	if err != nil {
		return nil, fmt.Errorf("tools.toml: %w", err)
	}
	out := map[string]Profile{}
	for i, d := range []conf.Doc{def, user, local} {
		local := i == 2
		for name, sec := range d {
			if name == "" {
				continue
			}
			// A [profile.<name>] section in the local config is the only layer
			// that can invent a profile, so a typo there would otherwise create
			// a nameless, command-less profile that spawns into a dead pane.
			// Only honour a local-only section when it actually sets a command.
			if _, known := out[name]; local && !known && conf.Str(sec, "command") == "" {
				logx.For("tools").Warn("ignoring local profile section: no command and no such tool profile",
					"profile", name)
				continue
			}
			p := out[name]
			p.Name = name
			apply(&p, sec)
			if p.Patterns == "" {
				p.Patterns = name
			}
			out[name] = p
		}
	}
	return out, nil
}

// apply overlays one TOML section onto a profile; absent keys are left alone.
func apply(p *Profile, sec map[string]conf.Value) {
	if v := conf.Str(sec, "command"); v != "" {
		p.Command = v
	}
	if v, ok := sec["args"]; ok {
		p.Args, _ = v.([]string)
	}
	if v := conf.Map(sec, "env"); v != nil {
		p.Env = v
	}
	if v, ok := sec["launch_id"]; ok {
		p.LaunchID, _ = v.([]string)
	}
	if v, ok := sec["resume"]; ok {
		p.Resume, _ = v.([]string)
	}
	if v := conf.Str(sec, "patterns"); v != "" {
		p.Patterns = v
	}
	if v, ok := sec["session_path"]; ok {
		p.SessionPath, _ = v.(string)
	}
	if v, ok := sec["subagent_log"]; ok {
		p.SubagentLog, _ = v.(string)
	}
	if v, ok := sec["prompt_args"]; ok {
		p.PromptArgs, _ = v.([]string)
	}
	if v := conf.Map(sec, "mode_args"); v != nil {
		p.ModeArgs = splitModes(v)
	}
	if v := conf.Map(sec, "mode_keys"); v != nil {
		p.ModeKeys = splitModes(v)
	}
	if v, ok := sec["ready"]; ok {
		p.Ready, _ = v.(string)
	}
	if v, ok := sec["peer_args"]; ok {
		p.PeerArgs, _ = v.([]string)
	}
	if v, ok := sec["hook_args"]; ok {
		p.HookArgs, _ = v.([]string)
	}
}

// splitModes turns an inline table of mode -> "a b c" into mode -> ["a","b","c"].
// The conf TOML subset types inline tables as map[string]string, so a whole arg
// list is written as one space-separated string. Limitation (documented in
// DESIGN.md §4, README.md and examples/config.toml.example): an argument that
// itself contains a space cannot be expressed this way.
func splitModes(m map[string]string) map[string][]string {
	out := map[string][]string{}
	for k, v := range m {
		out[k] = strings.Fields(v)
	}
	return out
}

// Names returns sorted profile names.
func Names(ps map[string]Profile) []string {
	var n []string
	for k := range ps {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// Vars are substituted into command and args. PeerMCP and PeerMCPClaude are
// the peer MCP configs (see PeerMCP); when both are empty PeerArgs are left
// out. Hooks is the status-hook settings (see ClaudeHooks); when empty
// HookArgs are left out.
type Vars struct{ Fleet, Name, ID, PeerMCP, PeerMCPClaude, Hooks string }

func (v Vars) expand(s string) string {
	return strings.NewReplacer("{fleet}", v.Fleet, "{name}", v.Name, "{id}", v.ID,
		"{peer_mcp_claude}", v.PeerMCPClaude, "{peer_mcp}", v.PeerMCP, "{hooks}", v.Hooks).Replace(s)
}

// hookEvents are the Claude Code events worth a status update: the turn
// ending, a prompt or idle nudge, and the start of work. More would only
// repeat "working".
var hookEvents = []string{"Stop", "Notification", "UserPromptSubmit", "PreToolUse", "SessionStart"}

// ClaudeHooks is a Claude Code settings document that calls
// `exe hook <event> --agent name` on each of hookEvents. It is passed to
// `claude --settings`, which merges it over the user's own settings for that
// one process only (ADR-49): fleet never edits ~/.claude/settings.json.
func ClaudeHooks(exe, name string) string {
	hooks := map[string]any{}
	for _, e := range hookEvents {
		cmd := map[string]any{"type": "command",
			"command": Quote(exe) + " hook " + strings.ToLower(e) + " --agent " + Quote(name)}
		hooks[e] = []any{map[string]any{"hooks": []any{cmd}}}
	}
	b, _ := json.Marshal(map[string]any{"hooks": hooks})
	return string(b)
}

// PeerMCP returns the one-server MCP configs that run `exe mcp --agent name`
// as "fleet-peer": copilot's schema (type local, tools) and Claude's (stdio).
func PeerMCP(exe, name string) (copilot, claude string) {
	args := []string{"mcp", "--agent", name}
	cp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"fleet-peer": map[string]any{
		"type": "local", "command": exe, "args": args, "tools": []string{"*"}}}})
	cl, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"fleet-peer": map[string]any{
		"type": "stdio", "command": exe, "args": args}}})
	return string(cp), string(cl)
}

// AgentVars are the Vars for launching agent name: fleet exe, id, peer MCP
// and status hooks.
func AgentVars(exe, name, id string) Vars {
	cp, cl := PeerMCP(exe, name)
	return Vars{Fleet: exe, Name: name, ID: id, PeerMCP: cp, PeerMCPClaude: cl, Hooks: ClaudeHooks(exe, name)}
}

// Launch describes everything beyond the profile that shapes one command line.
type Launch struct {
	Resume bool     // use Resume args instead of LaunchID
	Mode   string   // name of a ModeArgs/ModeKeys entry; "" = the tool's default
	Extra  []string // extra args from local preferences
	Prompt string   // initial prompt, delivered via PromptArgs when the profile has them
	// NoPromptArgs forbids carrying the prompt in argv even when the profile
	// has PromptArgs. Set when auto_submit is off: `copilot -i <prompt>`
	// submits the prompt itself, so honouring "paste but don't press Enter"
	// means falling back to the paste path.
	NoPromptArgs bool
}

// Argv builds the launch argv. embedded reports whether the prompt was placed
// in the argv (PromptArgs); when false the caller must paste it after the pane
// is ready. An unknown mode is ignored (see Profile.HasMode).
func (p Profile) Argv(v Vars, l Launch) (argv []string, embedded bool) {
	argv = []string{v.expand(p.Command)}
	if v.PeerMCP != "" || v.PeerMCPClaude != "" {
		for _, a := range p.PeerArgs {
			argv = append(argv, v.expand(a))
		}
	}
	if v.Hooks != "" {
		for _, a := range p.HookArgs {
			argv = append(argv, v.expand(a))
		}
	}
	for _, a := range p.Args {
		argv = append(argv, v.expand(a))
	}
	for _, a := range p.ModeArgs[l.Mode] {
		argv = append(argv, v.expand(a))
	}
	switch {
	case l.Resume:
		for _, a := range p.Resume {
			argv = append(argv, v.expand(a))
		}
	case v.ID != "":
		for _, a := range p.LaunchID {
			argv = append(argv, v.expand(a))
		}
	}
	argv = append(argv, l.Extra...)
	if l.Prompt != "" && len(p.PromptArgs) > 0 && !l.NoPromptArgs {
		for _, a := range p.PromptArgs {
			argv = append(argv, strings.ReplaceAll(v.expand(a), "{prompt}", l.Prompt))
		}
		embedded = true
	}
	return argv, embedded
}

// SessionGlob returns SessionPath with {id} substituted and a leading ~ expanded.
func (p Profile) SessionGlob(id string) string { return expandPath(p.SessionPath, id) }

// SubagentLogPath returns SubagentLog for a session id ("" when unset or id is empty).
func (p Profile) SubagentLogPath(id string) string {
	if p.SubagentLog == "" || id == "" {
		return ""
	}
	return expandPath(p.SubagentLog, id)
}

func expandPath(s, id string) string {
	g := strings.ReplaceAll(s, "{id}", id)
	if g == "~" || strings.HasPrefix(g, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			g = home + g[1:]
		}
	}
	return g
}

// SessionMatches returns the paths where the session id is stored (nil when
// SessionPath is unset or nothing matches).
func (p Profile) SessionMatches(id string) []string {
	if p.SessionPath == "" || id == "" {
		return nil
	}
	m, _ := filepath.Glob(p.SessionGlob(id))
	return m
}

// ShellCommand wraps argv into `bash -lc 'exec ...'` so login PATH applies
// (aliases don't work in non-interactive spawns; profiles replace them).
func ShellCommand(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = Quote(a)
	}
	return "bash -lc " + Quote("exec "+strings.Join(q, " "))
}

// EnvList returns KEY=VAL pairs including a PATH with ~/.local/bin first.
func (p Profile) EnvList() []string {
	home, _ := os.UserHomeDir()
	var out []string
	keys := make([]string, 0, len(p.Env))
	for k := range p.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+os.ExpandEnv(p.Env[k]))
	}
	return append(out, "PATH="+filepath.Join(home, ".local/bin")+":"+os.Getenv("PATH"))
}

// Quote single-quotes s for sh.
func Quote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '.' || r == '/' || r == '=' || r == ':' || r == '{' || r == '}' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// NewUUID returns a random v4 UUID.
func NewUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
