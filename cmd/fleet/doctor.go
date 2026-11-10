package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/sturlabragason/fleet/internal/conf"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/tools"
)

// minTmux is the oldest tmux fleet runs on: display-popup -b/-T (the
// changes, keys and peek popups) came in tmux 3.3.
var minTmux = [2]int{3, 3}

const (
	levelOK   = "ok"
	levelWarn = "warn"
	levelFail = "fail"
)

type doctorCheck struct {
	Name   string `json:"name"`
	Level  string `json:"level"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// doctorEnv is what fleet doctor reads from the machine, so tests can replace it.
type doctorEnv struct {
	home, userHome, exe string
	getenv              func(string) string
	tmuxVersion         func() (string, error)
	look                func(cmd string) error // nil: as spawn looks (fleet.CheckCommand)
}

func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return errors.New("usage: fleet doctor [--json]")
	}
	userHome, _ := os.UserHomeDir()
	exe, _ := os.Executable()
	env := doctorEnv{home: logx.Home(), userHome: userHome, exe: exe, getenv: os.Getenv, tmuxVersion: tmuxVersion}
	return writeDoctor(os.Stdout, doctor(env), *asJSON)
}

// tmuxVersion runs `tmux -V`, which prints the version without starting a server.
func tmuxVersion() (string, error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return "", err
	}
	out, err := exec.Command("tmux", "-V").Output()
	return strings.TrimSpace(string(out)), err
}

func writeDoctor(w io.Writer, cs []doctorCheck, asJSON bool) error {
	if asJSON {
		b, err := json.MarshalIndent(cs, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(b))
	} else {
		for _, c := range cs {
			line := fmt.Sprintf("%-4s  %s: %s", c.Level, c.Name, c.Detail)
			if c.Fix != "" {
				line += " · fix: " + c.Fix
			}
			fmt.Fprintln(w, line)
		}
	}
	if slices.ContainsFunc(cs, func(c doctorCheck) bool { return c.Level == levelFail }) {
		return exitCode(1)
	}
	return nil
}

func doctor(env doctorEnv) []doctorCheck {
	cs := []doctorCheck{checkTmux(env.tmuxVersion), checkLocale(env.getenv), checkLocalBin(env)}
	pr, c := checkConfig(env)
	cs = append(cs, c)
	userTools, profiles, c := checkTools(env, pr)
	cs = append(cs, c)
	if profiles == nil {
		cs = append(cs, doctorCheck{Name: "profiles", Level: levelWarn, Detail: "not checked: tools.toml does not load"})
	} else {
		cs = append(cs, checkProfiles(env, pr, userTools, profiles)...)
	}
	return cs
}

var tmuxVersionRe = regexp.MustCompile(`^tmux (?:next-)?(\d+)\.(\d+)`)

func checkTmux(version func() (string, error)) doctorCheck {
	need := fmt.Sprintf("%d.%d", minTmux[0], minTmux[1])
	c := doctorCheck{Name: "tmux"}
	v, err := version()
	if err != nil || v == "" {
		c.Level, c.Detail, c.Fix = levelFail, "not found on PATH", "install tmux "+need+" or newer"
		return c
	}
	m := tmuxVersionRe.FindStringSubmatch(v)
	if m == nil {
		c.Level, c.Detail = levelWarn, fmt.Sprintf("%q: can't read the version; fleet needs %s or newer", v, need)
		return c
	}
	maj, _ := strconv.Atoi(m[1])
	mnr, _ := strconv.Atoi(m[2])
	if maj < minTmux[0] || maj == minTmux[0] && mnr < minTmux[1] {
		c.Level, c.Detail, c.Fix = levelFail, fmt.Sprintf("%s is too old; fleet needs %s or newer", v, need), "upgrade tmux"
		return c
	}
	c.Level, c.Detail = levelOK, fmt.Sprintf("%s (fleet needs %s or newer)", v, need)
	return c
}

// checkLocale warns only: fleet runs tmux with -u (#21), but the agents'
// panes inherit the locale, and a tool may draw its screen in ASCII under it.
func checkLocale(getenv func(string) string) doctorCheck {
	c := doctorCheck{Name: "locale"}
	name, val := "LANG", getenv("LANG")
	for _, v := range []string{"LC_ALL", "LC_CTYPE"} {
		if s := getenv(v); s != "" {
			name, val = v, s
			break
		}
	}
	l := strings.ToLower(val)
	if strings.Contains(l, "utf-8") || strings.Contains(l, "utf8") {
		c.Level, c.Detail = levelOK, name+"="+val
		return c
	}
	if val == "" {
		val = "(unset)"
	}
	c.Level = levelWarn
	c.Detail = name + "=" + val + " is not UTF-8; fleet forces tmux to UTF-8, but the agents' panes inherit this"
	c.Fix = "export " + name + "=C.UTF-8 in your shell profile"
	return c
}

func checkLocalBin(env doctorEnv) doctorCheck {
	dir := filepath.Join(env.userHome, ".local", "bin")
	c := doctorCheck{Name: "PATH"}
	if slices.ContainsFunc(filepath.SplitList(env.getenv("PATH")), func(d string) bool { return d != "" && filepath.Clean(d) == dir }) {
		c.Level, c.Detail = levelOK, "~/.local/bin is on PATH"
		return c
	}
	c.Level, c.Detail = levelWarn, "~/.local/bin is not on PATH; fleet and the agent CLIs installed there won't run from a shell"
	c.Fix = `export PATH="$HOME/.local/bin:$PATH" in your shell profile`
	return c
}

// tildePath shows a path under the user's home as ~/...
func tildePath(env doctorEnv, p string) string {
	if env.userHome != "" && strings.HasPrefix(p, env.userHome+string(filepath.Separator)) {
		return "~" + p[len(env.userHome):]
	}
	return p
}

func checkConfig(env doctorEnv) (*prefs.Prefs, doctorCheck) {
	path := prefs.Path()
	c := doctorCheck{Name: "config.toml"}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		c.Level, c.Detail = levelOK, "none at "+tildePath(env, path)+"; using defaults"
		return nil, c
	}
	pr, err := prefs.Load()
	if err != nil {
		c.Level, c.Detail, c.Fix = levelFail, err.Error(), "fix the file; see examples/config.toml.example"
		return nil, c
	}
	c.Level, c.Detail = levelOK, tildePath(env, path)+" parses"
	return pr, c
}

// checkTools returns the profile names ~/.fleet/tools.toml defines and the
// loaded profiles (nil when they don't load).
func checkTools(env doctorEnv, pr *prefs.Prefs) ([]string, map[string]tools.Profile, doctorCheck) {
	path := tools.Path(env.home)
	c := doctorCheck{Name: "tools.toml"}
	doc, err := conf.ParseFile(path)
	if err != nil {
		c.Level, c.Detail, c.Fix = levelFail, tildePath(env, path)+": "+err.Error(), "fix the file; fleet profiles shows what loads"
		return nil, nil, c
	}
	ps, err := tools.LoadWith(env.home, pr.ToolsDoc())
	if err != nil {
		c.Level, c.Detail, c.Fix = levelFail, err.Error(), "fix the file; fleet profiles shows what loads"
		return nil, nil, c
	}
	var names []string
	for n := range doc {
		if n != "" {
			names = append(names, n)
		}
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		c.Level, c.Detail = levelOK, "none at "+tildePath(env, path)+"; built-in profiles only"
	} else {
		c.Level, c.Detail = levelOK, tildePath(env, path)+" parses"
	}
	return names, ps, c
}

// checkProfiles checks the command of every profile the user defines (in
// tools.toml or a [profile.<name>] in config.toml) and of the admiral's
// profile, as a spawn (or, for the admiral, fleet up) would with
// fleet.CheckCommand (#23). The admiral's is a fail, since fleet up can't
// start without it; another is a warn.
func checkProfiles(env doctorEnv, pr *prefs.Prefs, userTools []string, ps map[string]tools.Profile) []doctorCheck {
	f := &fleet.Fleet{Home: env.home, Exe: env.exe, Profiles: ps, Prefs: pr, LookCmd: env.look}
	admiral, _, _ := resolveAdmiralProfile("", ps, env.getenv, supervisor.LoadChoiceOf(env.home, supervisor.Main), pr.AdmiralProfile())
	names := append([]string{admiral}, userTools...)
	for n := range pr.ToolsDoc() {
		names = append(names, n)
	}
	slices.Sort(names[1:])
	var cs []doctorCheck
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		c := doctorCheck{Name: "profile " + n}
		p, ok := ps[n]
		if !ok {
			// tools.LoadWith drops it: a local-only section with no command.
			c.Level, c.Detail = levelWarn, "config.toml [profile."+n+"] sets no command and is no tool profile, so fleet ignores it"
			c.Fix = "set its command, or fix the name (fleet profiles lists them)"
			cs = append(cs, c)
			continue
		}
		// The admiral always runs the tool's own screen (fleet up).
		driver, role := f.DefaultDriver(n), ""
		if n == admiral {
			driver, role = prefs.DriverTmux, ", the admiral's"
		}
		if err := f.CheckCommand(n, p, driver); err != nil {
			c.Level, c.Detail = levelWarn, err.Error()+" (driver "+driver+role+")"
			c.Fix = "install it, or set its command in tools.toml"
			if n == admiral {
				c.Level = levelFail
				c.Fix += ", or pick another admiral: fleet --admiral-profile NAME up"
			}
		} else {
			cmd := p.Command
			if driver == prefs.DriverACP && len(p.ACPCommand) > 0 {
				cmd = p.ACPCommand[0]
			}
			c.Level, c.Detail = levelOK, strings.ReplaceAll(cmd, "{fleet}", "fleet")+" found (driver "+driver+role+")"
		}
		cs = append(cs, c)
	}
	return cs
}
