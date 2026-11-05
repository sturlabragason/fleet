package prefs

import (
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/conf"
	"github.com/sturlabragason/fleet/internal/summary"
)

// Summary is the [summary] section: the cheap model behind `fleet peek
// --summary` and MCP fleet_peek summary=true (DESIGN §6.2, ADR-40).
type Summary struct {
	Enabled     bool          // summary = false turns the feature off
	Model       string        // model name passed to the CLI
	Command     string        // CLI to run (default copilot)
	Args        []string      // argv with {prompt} and {model}
	MinInterval time.Duration // shortest gap between calls for one agent
	// ("0" in the file means summary.NoRateLimit: no gap at all)
	Timeout time.Duration // how long one call may take
}

// DefaultSummary is the built-in configuration: Haiku 5.5 through the
// installed Copilot CLI, which brings its own authentication.
func DefaultSummary() Summary {
	return Summary{
		Enabled:     true,
		Model:       summary.DefaultModel,
		Command:     summary.DefaultCommand,
		Args:        summary.DefaultArgs(),
		MinInterval: summary.DefaultMinInterval,
		Timeout:     summary.DefaultTimeout,
	}
}

// Summary returns the effective [summary] settings.
func (p *Prefs) Summary() Summary {
	if p == nil {
		return DefaultSummary()
	}
	return p.summary
}

func applySummary(s Summary, sec map[string]conf.Value) Summary {
	if v, ok := conf.Bool(sec, "enabled"); ok {
		s.Enabled = v
	}
	if v := strings.TrimSpace(conf.Str(sec, "model")); v != "" {
		s.Model = v
	}
	if v := strings.TrimSpace(conf.Str(sec, "command")); v != "" {
		s.Command = v
	}
	if v, ok := sec["args"].([]string); ok && len(v) > 0 {
		s.Args = v
	}
	if v, ok := conf.Dur(sec, "min_interval"); ok && v >= 0 {
		s.MinInterval = v
		if v == 0 {
			s.MinInterval = summary.NoRateLimit
		}
	}
	if v, ok := conf.Dur(sec, "timeout"); ok && v > 0 {
		s.Timeout = v
	}
	return s
}
