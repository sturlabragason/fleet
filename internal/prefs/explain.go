package prefs

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/conf"
)

// Setting is one effective spawn preference and where it came from.
type Setting struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source string `json:"source"` // "[profile.<name>]", "[defaults]" or "built-in"
}

// spawnKeys are the config.toml keys applySpawn reads, with the effective
// value of each.
var spawnKeys = []struct {
	key string
	val func(Spawn) string
}{
	{"mode", func(s Spawn) string { return orNone(s.Mode, "tool's own") }},
	{"auto_submit", func(s Spawn) string { return fmt.Sprint(s.AutoSubmit) }},
	{"ready_timeout", func(s Spawn) string { return s.ReadyTimeout.String() }},
	{"extra_args", func(s Spawn) string { return orNone(strings.Join(s.ExtraArgs, " "), "none") }},
	{"env", nil},
	{"playbook", func(s Spawn) string { return fmt.Sprint(s.Playbook) }},
	{"worktree", func(s Spawn) string { return fmt.Sprint(s.Worktree) }},
	{"auto_trust", func(s Spawn) string { return fmt.Sprint(s.AutoTrust) }},
	{"fence_writes", func(s Spawn) string { return fmt.Sprint(s.FenceWrites) }},
	{"keep_mcp", func(s Spawn) string { return orNone(strings.Join(s.KeepMCP, ", "), "none") }},
	{"auto_close_done", func(s Spawn) string { return fmt.Sprint(s.AutoCloseDone) }},
	{"auto_close_idle", func(s Spawn) string { return offDur(s.AutoCloseIdle) }},
	{"dead_pane_grace", func(s Spawn) string {
		if s.DeadPaneGrace == GraceOff {
			return "off"
		}
		return s.DeadPaneGrace.String()
	}},
	{"stuck_after", func(s Spawn) string { return offDur(s.StuckAfter) }},
	{"stuck_action", func(s Spawn) string { return s.StuckAction }},
}

func orNone(s, none string) string {
	if s == "" {
		return "(" + none + ")"
	}
	return s
}

func offDur(d time.Duration) string {
	if d == 0 {
		return "off"
	}
	return d.String()
}

// Explain lists profile's effective spawn preferences, each with the
// highest layer that changed it: [profile.<name>] over [defaults] over
// fleet's built-in default (For). A key a section sets to a value applySpawn
// refuses is not that section's, so the answer is what a spawn does; env is
// one row per variable, names only. The driver is not here: it has more
// layers than config.toml (fleet.ExplainDriver).
func (p *Prefs) Explain(profile string) []Setting {
	base, def := Neutral(), Neutral()
	var prof map[string]conf.Value
	if p != nil {
		def, prof = p.defaults, p.profiles[profile]
	}
	eff := applySpawn(def, prof)
	layer := func(b, d, e string) string {
		switch {
		case e != d:
			return "[profile." + profile + "]"
		case d != b:
			return "[defaults]"
		}
		return "built-in"
	}
	out := make([]Setting, 0, len(spawnKeys))
	for _, k := range spawnKeys {
		if k.key == "env" {
			out = append(out, envSettings(base.Env, def.Env, eff.Env, layer)...)
			continue
		}
		out = append(out, Setting{k.key, k.val(eff), layer(k.val(base), k.val(def), k.val(eff))})
	}
	return out
}

// envSettings is one "env.<NAME>" row per variable of the effective env,
// with the layer that set it; values are never shown, they may be secrets.
func envSettings(base, def, eff map[string]string, layer func(b, d, e string) string) []Setting {
	if len(eff) == 0 {
		return []Setting{{"env", "(none)", "built-in"}}
	}
	ks := make([]string, 0, len(eff))
	for k := range eff {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	out := make([]Setting, 0, len(ks))
	for _, k := range ks {
		b, bok := base[k]
		d, dok := def[k]
		e := eff[k]
		out = append(out, Setting{"env." + k, "(set)", layer(fmt.Sprint(bok, b), fmt.Sprint(dok, d), fmt.Sprint(true, e))})
	}
	return out
}
