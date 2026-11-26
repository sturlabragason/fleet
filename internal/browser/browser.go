// Package browser is the browser-open guard every spawned agent runs under
// (ADR-200). Shims for xdg-open, gio, sensible-browser and the browsers
// themselves sit first on the agent's PATH, and $BROWSER points at one.
// Each shim runs `fleet browser-guard <name> args…`, which opens the URL in
// the default browser unless the user is presenting (fleet present on, or a
// screen share or call auto-detected) or the agent is on the never-open
// list (personal, legal, tax, health …). A blocked open is logged and
// handed over once the user stops presenting.
package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Names are the openers that get a shim: what agents, scripts and
// libraries (Python's webbrowser, npm open) run to show a page.
var Names = []string{
	"xdg-open", "gio", "sensible-browser", "x-www-browser", "www-browser",
	"firefox", "chromium", "chromium-browser", "google-chrome", "google-chrome-stable",
	"brave", "brave-browser",
}

// BlockedMsg is what an agent reads when its open is refused.
const BlockedMsg = "browser open blocked by fleet: give the URL in your report instead"

// DefaultNeverOpen is [browser] never_open without a config: agents whose
// name, route or profile carries one of these words never open a page.
var DefaultNeverOpen = []string{"personal", "legal", "tax", "health"}

// Dir is the guard's shim directory under the fleet home.
func Dir(home string) string { return filepath.Join(home, "guard", "bin") }

// Install writes the shims into Dir(home), each running exe's guard. It
// rewrites only what changed, so every spawn can call it.
func Install(home, exe string) error {
	if runtime.GOOS == "windows" {
		return nil // shortcut: sh shims only; Windows agents are unguarded until someone runs fleet there
	}
	dir := Dir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("browser guard dir: %w", err)
	}
	for _, n := range Names {
		body := "#!/bin/sh\nexec " + shQuote(exe) + " browser-guard " + n + " \"$@\"\n"
		p := filepath.Join(dir, n)
		if b, err := os.ReadFile(p); err == nil && string(b) == body {
			continue
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			return fmt.Errorf("browser guard shim %s: %w", n, err)
		}
	}
	return nil
}

// Env puts the guard dir first in env's PATH entry and points BROWSER at
// the xdg-open shim. env is a spawn's KEY=VAL list with one PATH entry.
func Env(env []string, home string) []string {
	if runtime.GOOS == "windows" {
		return env
	}
	dir := Dir(home)
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "PATH="); ok {
			e = "PATH=" + dir + string(os.PathListSeparator) + v
		}
		if !strings.HasPrefix(e, "BROWSER=") {
			out = append(out, e)
		}
	}
	return append(out, "BROWSER="+filepath.Join(dir, "xdg-open"))
}

// RealEnv is env for the real opener: no BROWSER and no guard dir on PATH,
// so an opener that falls back to $BROWSER or runs gio can't re-enter the
// guard.
func RealEnv(env []string, home string) []string {
	dir := Dir(home)
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "BROWSER=") {
			continue
		}
		if v, ok := strings.CutPrefix(e, "PATH="); ok {
			var keep []string
			for _, d := range filepath.SplitList(v) {
				if filepath.Clean(d) != dir {
					keep = append(keep, d)
				}
			}
			e = "PATH=" + strings.Join(keep, string(os.PathListSeparator))
		}
		out = append(out, e)
	}
	return out
}

// Agent is what the guard knows about the agent asking to open.
type Agent struct{ Name, Route, Profile string }

// Verdict says whether an open goes ahead, and why not.
type Verdict struct {
	Open   bool
	Reason string // "presenting: …" or "never-open: …"; "" when Open
}

// Detector reports a presenting signal ("" = none). Tests stub it.
type Detector func(ctx context.Context) string

// Decide applies the guard: presenting blocks every open, the never-open
// list blocks the agent's, everything else opens.
func Decide(ctx context.Context, home string, a Agent, never []string, detect Detector) Verdict {
	if why := Presenting(ctx, home, detect); why != "" {
		return Verdict{Reason: "presenting: " + why}
	}
	if w := NeverMatch(a, never); w != "" {
		return Verdict{Reason: "never-open: " + w}
	}
	return Verdict{Open: true}
}

// NeverMatch returns the never-open word found at a word start in the
// agent's name, route or profile ("gjald" matches gjald-letter and
// gjaldthrot, "tax" doesn't match syntax), or "".
func NeverMatch(a Agent, never []string) string {
	s := strings.ToLower(a.Name + " " + a.Route + " " + a.Profile)
	for _, w := range never {
		w = strings.ToLower(strings.TrimSpace(w))
		if w != "" && regexp.MustCompile(`(^|[^a-z0-9])`+regexp.QuoteMeta(w)).MatchString(s) {
			return w
		}
	}
	return ""
}

// URL picks what an opener was asked to show, for the log: the first
// argument that is not a flag (after "open" for gio).
func URL(name string, args []string) string {
	if name == "gio" && len(args) > 0 && args[0] == "open" {
		args = args[1:]
	}
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return strings.Join(args, " ")
}

// Guarded reports whether the call opens something: everything but gio's
// other subcommands (gio list, gio info …), which pass straight through.
func Guarded(name string, args []string) bool {
	return name != "gio" || (len(args) > 0 && args[0] == "open")
}

// Real returns the argv that opens for real: the opener on path outside
// the guard dir, or, for one that isn't installed (no sensible-browser on
// Fedora), xdg-open with the URL alone.
func Real(name string, args []string, path, home string) ([]string, error) {
	if p := lookPath(name, path, Dir(home)); p != "" {
		return append([]string{p}, args...), nil
	}
	if p := lookPath("xdg-open", path, Dir(home)); p != "" && name != "gio" {
		return []string{p, URL(name, args)}, nil
	}
	return nil, fmt.Errorf("%s: not found on PATH", name)
}

func lookPath(name, path, skip string) string {
	for _, d := range filepath.SplitList(path) {
		if d == "" || filepath.Clean(d) == skip {
			continue
		}
		p := filepath.Join(d, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
