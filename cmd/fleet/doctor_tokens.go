package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sturlabragason/fleet/internal/rules"
)

// Token checks (ADR-195): what every agent pays for on every call.

// rulesCap is the size the standing user rules should stay under: every
// spawn's first prompt carries them, and every later call re-reads them.
const rulesCap = 4096

// checkRules warns when the user's standing rules outgrow rulesCap.
func checkRules(home string) (doctorCheck, bool) {
	rs, err := rules.List(rules.UserFile(home))
	if err != nil || len(rs) == 0 {
		return doctorCheck{}, false
	}
	n := 0
	for _, r := range rs {
		n += len(r) + 3 // "- " and the newline
	}
	c := doctorCheck{Name: "standing rules", Level: levelOK, Detail: fmt.Sprintf("%d rules, %d bytes (cap %d)", len(rs), n, rulesCap)}
	if n > rulesCap {
		c.Level = levelWarn
		c.Fix = "merge or shorten rules in " + rules.UserFile(home) + ": every spawn carries them on every call"
	}
	return c, true
}

// checkRTK says whether rtk, which shortens shell output before an agent
// reads it, is installed and hooked into Claude Code and Copilot CLI. It
// only reads files: fleet never runs or installs rtk.
func checkRTK(userHome string, look func(string) error) (doctorCheck, bool) {
	if userHome == "" || look == nil || look("rtk") != nil {
		return doctorCheck{}, false
	}
	var on, off []string
	for _, h := range []struct{ name, glob string }{
		{"claude", filepath.Join(userHome, ".claude", "settings.json")},
		{"copilot", filepath.Join(userHome, ".copilot", "hooks", "*.json")},
	} {
		if mentions(h.glob, "rtk") {
			on = append(on, h.name)
		} else {
			off = append(off, h.name)
		}
	}
	c := doctorCheck{Name: "rtk", Level: levelOK, Detail: "hook on for " + strings.Join(on, ", ")}
	if len(off) > 0 {
		c.Level = levelInfo
		c.Detail = "installed, no hook for " + strings.Join(off, ", ") + ": agents read shell output unfiltered"
		c.Fix = "rtk init -g (Claude); a Copilot preToolUse hook in ~/.copilot/hooks"
	}
	return c, true
}

// mentions reports whether any file matching glob contains s.
func mentions(glob, s string) bool {
	ps, _ := filepath.Glob(glob) // the pattern is ours and valid
	for _, p := range ps {
		if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), s) {
			return true
		}
	}
	return false
}
