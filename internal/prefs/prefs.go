// Package prefs reads the local, never-committed spawn preferences from
// $XDG_CONFIG_HOME/fleet/config.toml (default ~/.config/fleet/config.toml,
// FLEET_CONFIG overrides the whole path). Repo defaults stay neutral: without
// the file fleet spawns in whatever mode the tool itself defaults to.
package prefs

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/conf"
)

// DefaultReadyTimeout is how long spawn waits for a TUI to accept input.
// Spawn is synchronous for up to this long, including the MCP `fleet_spawn`
// tool call, so it is kept comfortably under a typical MCP client tool
// timeout rather than at the 30s mark.
const DefaultReadyTimeout = 15 * time.Second

// Spawn holds the effective preferences for one profile.
type Spawn struct {
	Mode         string        // "" = leave the tool's default
	AutoSubmit   bool          // press Enter after a pasted prompt
	ReadyTimeout time.Duration // how long to wait for the TUI
	ExtraArgs    []string      // appended to the launch argv
	// AutoCloseDone closes an agent that reports done with a clean, merged
	// work tree (DESIGN §6.7).
	AutoCloseDone bool
	// AutoCloseIdle, with AutoCloseDone, closes a done agent that has sat
	// idle this long once its work passes the auto-close check (0 = off).
	AutoCloseIdle time.Duration
	// Env is added to the agent pane's environment, over the profile's own
	// env. [profile.<name>] keys override [defaults] keys one by one.
	Env map[string]string
	// Playbook prepends the fleet worker playbook to a spawn's first prompt
	// (DESIGN §4, ADR-22).
	Playbook bool
	// DeadPaneGrace is how long a crashed agent's dead pane stays before it
	// is closed (DESIGN §6.7, ADR-33): 0 = at once, GraceOff = never.
	DeadPaneGrace time.Duration
	// Worktree gives a spawn into a git repo its own worktree and branch
	// (DESIGN §4, ADR-39). false spawns straight into the dir.
	Worktree bool
}

// GraceOff is DeadPaneGrace for dead_pane_grace = "off".
const GraceOff time.Duration = -1

// DefaultDeadPaneGrace is dead_pane_grace when unset.
const DefaultDeadPaneGrace = 5 * time.Minute

// Neutral is the built-in default: no mode, auto-submit, DefaultReadyTimeout,
// worker playbook on, DefaultDeadPaneGrace, a worktree per agent.
func Neutral() Spawn {
	return Spawn{AutoSubmit: true, ReadyTimeout: DefaultReadyTimeout, Playbook: true, DeadPaneGrace: DefaultDeadPaneGrace, Worktree: true}
}

// DefaultMaxWorking caps how many agents may be working at once before new
// spawns queue (DESIGN §7a). 0 in config.toml turns the cap off.
const DefaultMaxWorking = 4

// Prefs is a loaded config file.
type Prefs struct {
	Path       string // file it came from (may not exist)
	defaults   Spawn
	profiles   map[string]map[string]conf.Value
	maxWorking int
	admiral    string
	agentMail  string
	summary    Summary
}

// AdmiralProfile is [admiral] profile: the admiral's tool profile when no
// flag, env or last choice names one (DESIGN §6.10). "" = unset.
func (p *Prefs) AdmiralProfile() string {
	if p == nil {
		return ""
	}
	return p.admiral
}

// MaxWorking is [defaults] max_working: the working-agent cap, 0 = no cap.
func (p *Prefs) MaxWorking() int {
	if p == nil {
		return DefaultMaxWorking
	}
	return p.maxWorking
}

// Load reads the config file. A missing file is not an error.
func Load() (*Prefs, error) {
	path := Path()
	d, err := conf.ParseFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	p := &Prefs{Path: path, defaults: Neutral(), profiles: map[string]map[string]conf.Value{}, maxWorking: DefaultMaxWorking, summary: DefaultSummary()}
	if sec, ok := d["defaults"]; ok {
		p.defaults = applySpawn(p.defaults, sec)
		if v := conf.Str(sec, "max_working"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("%s: max_working %q: want a whole number >= 0", path, v)
			}
			p.maxWorking = n
		}
		if p.agentMail, err = parseAgentMail(sec); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	if sec, ok := d["summary"]; ok {
		p.summary = applySummary(p.summary, sec)
	}
	if sec, ok := d["admiral"]; ok {
		p.admiral = strings.TrimSpace(conf.Str(sec, "profile"))
	}
	for name, sec := range d {
		if n, ok := strings.CutPrefix(name, "profile."); ok && n != "" {
			p.profiles[n] = sec
		}
	}
	return p, nil
}

// For returns the effective preferences for a profile:
// [profile.<name>] > [defaults] > built-in.
func (p *Prefs) For(profile string) Spawn {
	if p == nil {
		return Neutral()
	}
	s := p.defaults
	if sec, ok := p.profiles[profile]; ok {
		s = applySpawn(s, sec)
	}
	return s
}

// ToolsDoc returns the [profile.<name>] sections re-keyed to profile names, so
// tools.LoadWith can use them as a third profile override layer.
func (p *Prefs) ToolsDoc() conf.Doc {
	if p == nil {
		return nil
	}
	d := conf.Doc{}
	for name, sec := range p.profiles {
		d[name] = sec
	}
	return d
}

func applySpawn(s Spawn, sec map[string]conf.Value) Spawn {
	if v, ok := sec["mode"]; ok {
		s.Mode, _ = v.(string)
	}
	if v, ok := conf.Bool(sec, "auto_submit"); ok {
		s.AutoSubmit = v
	}
	if v, ok := conf.Bool(sec, "auto_close_done"); ok {
		s.AutoCloseDone = v
	}
	if v, ok := conf.Dur(sec, "auto_close_idle"); ok && v >= 0 {
		s.AutoCloseIdle = v
	}
	if v, ok := sec["dead_pane_grace"].(string); ok && strings.EqualFold(strings.TrimSpace(v), "off") {
		s.DeadPaneGrace = GraceOff
	} else if v, ok := conf.Dur(sec, "dead_pane_grace"); ok && v >= 0 {
		s.DeadPaneGrace = v
	}
	if v, ok := conf.Bool(sec, "playbook"); ok {
		s.Playbook = v
	}
	if v, ok := conf.Bool(sec, "worktree"); ok {
		s.Worktree = v
	}
	if v, ok := conf.Dur(sec, "ready_timeout"); ok && v > 0 {
		s.ReadyTimeout = v
	}
	if v, ok := sec["extra_args"]; ok {
		s.ExtraArgs, _ = v.([]string)
	}
	if m := conf.Map(sec, "env"); len(m) > 0 {
		env := make(map[string]string, len(s.Env)+len(m))
		for k, v := range s.Env {
			env[k] = v
		}
		for k, v := range m {
			env[k] = v
		}
		s.Env = env
	}
	return s
}
