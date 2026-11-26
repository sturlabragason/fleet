package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/sturlabragason/fleet/internal/theme"
)

// checkThemes is the "themes" line (ADR-201): the user's theme files and
// their problems (unknown roles, bad colours, contrast below the floors).
// No themes dir is no line. A problem warns, never fails: the theme still
// draws.
func checkThemes(env doctorEnv, dir string) (doctorCheck, bool) {
	c := doctorCheck{Name: "themes"}
	probs, err := theme.LoadDir(dir)
	if err != nil {
		c.Level, c.Detail = levelWarn, err.Error()
		return c, true
	}
	n := len(theme.User())
	if n == 0 && len(probs) == 0 {
		return c, false
	}
	if len(probs) == 0 {
		c.Level, c.Detail = levelOK, fmt.Sprintf("%d in %s, no problems", n, tildePath(env, dir))
		return c, true
	}
	var parts []string
	for _, name := range slices.Sorted(maps.Keys(probs)) {
		parts = append(parts, name+".toml: "+strings.Join(probs[name], "; "))
	}
	c.Level, c.Detail = levelWarn, strings.Join(parts, " · ")
	c.Fix = "edit the file; roles are " + strings.Join(theme.Roles(), ", ")
	return c, true
}
