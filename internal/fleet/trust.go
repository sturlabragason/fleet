package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/tools"
	"github.com/sturlabragason/fleet/internal/trust"
)

// ScratchRoot is where fleet puts the per-agent directory it starts an agent
// in when the asked-for dir may not be used (DESIGN §4, ADR-47).
const ScratchRoot = "scratch"

// scratchDir is this agent's own directory under ~/.fleet/scratch.
func (f *Fleet) scratchDir(name string) string {
	return filepath.Join(f.Home, ScratchRoot, name)
}

// safeDir keeps an agent out of the home directory and the filesystem root.
// An agent started in $HOME reads every dotfile as project config, trips each
// tool's own "this is a dangerous folder" guard, and shares one project
// identity with every other agent; so fleet starts it in ~/.fleet/scratch/<name>
// instead and says so. Any other dir is returned unchanged.
//
// redirected is true when the dir was moved, for the spawn reply.
func (f *Fleet) safeDir(name, dir string) (safe string, redirected bool) {
	if !isHomeOrRoot(dir) {
		return dir, false
	}
	s := f.scratchDir(name)
	if err := os.MkdirAll(s, 0o700); err != nil {
		logx.For("fleet").Warn("scratch dir failed; starting in the dir asked for",
			"agent", name, "dir", dir, "scratch", s, "err", err)
		return dir, false
	}
	logx.For("fleet").Info("not starting an agent in the home directory", "agent", name, "asked", dir, "dir", s)
	return s, true
}

// RedirectNote explains a dir that is not the one the caller asked for.
func RedirectNote() string {
	return "fleet does not start an agent in the home directory, so this is a scratch dir of its own"
}

// InScratch reports whether dir is a per-agent scratch dir, i.e. the spawn
// was moved out of the home directory. Spawn replies say so.
func (f *Fleet) InScratch(dir string) bool {
	if dir == "" {
		return false
	}
	root := filepath.Join(f.Home, ScratchRoot)
	rel, err := filepath.Rel(root, filepath.Clean(dir))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// isHomeOrRoot reports whether dir is the user's home directory or /.
func isHomeOrRoot(dir string) bool {
	if dir == "" {
		return false
	}
	c := filepath.Clean(dir)
	if c == string(filepath.Separator) {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	if c == filepath.Clean(home) {
		return true
	}
	// A symlinked home ("/home/user" -> "/var/home/user") must still count.
	r1, err1 := filepath.EvalSymlinks(c)
	r2, err2 := filepath.EvalSymlinks(home)
	return err1 == nil && err2 == nil && r1 == r2
}

// TrustDir pre-marks dir as trusted in the tool's own config before the
// agent starts, so a trust dialog never stops it (DESIGN §4, ADR-47). It
// never fails a spawn: the worst case is the dialog fleet was avoiding, and
// the fallback answers that on screen.
//
// marked is true when the tool's own config now says the dir is trusted, so
// the caller knows the pane needs no watching.
func (f *Fleet) TrustDir(profile string, p tools.Profile, dir string) (marked bool) {
	log := logx.For("fleet")
	if !f.Prefs.For(profile).AutoTrust {
		log.Debug("auto-trust off for this profile", "profile", profile, "dir", dir)
		return false
	}
	r, err := trust.Mark(p.Command, f.agentEnv(profile, p), dir)
	switch {
	case errors.Is(err, trust.ErrUnsupported):
		log.Debug("no trust store for this tool", "profile", profile, "command", p.Command)
	case err != nil:
		log.Warn("auto-trust failed; the agent may ask about this folder",
			"profile", profile, "dir", dir, "err", err)
	case r.Changed:
		log.Info("trusted the agent's dir", "profile", profile, "tool", r.Tool, "dir", dir, "file", r.File)
		return true
	default:
		log.Debug("dir already trusted", "profile", profile, "tool", r.Tool, "dir", dir)
		return true
	}
	return false
}

// agentEnv is the environment the agent will get: the profile's env with the
// config file's [defaults]/[profile.<name>] env over it, the same merge
// start() does, so a profile's own CLAUDE_CONFIG_DIR is honoured.
func (f *Fleet) agentEnv(profile string, p tools.Profile) map[string]string {
	extra := f.Prefs.For(profile).Env
	if len(extra) == 0 {
		return p.Env
	}
	env := make(map[string]string, len(p.Env)+len(extra))
	for k, v := range p.Env {
		env[k] = v
	}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

// trustMenu matches a startup dialog that asks whether this folder and the
// files it pulls in are trusted. It is the fallback for what pre-marking
// cannot cover: a tool with no trust store of its own (agy), a config format
// that changed under us, or an agent started before fleet knew (ADR-47).
var trustMenu = regexp.MustCompile(`(?i)(do you trust this folder|do you trust the contents of this project|is this a project you created or one you trust|allow external claude\.md file imports)`)

// trustWindow caps the wait for that dialog. It is only ever reached when
// the pane never comes up at all; a tool that starts cleanly ends the watch
// as soon as its input box is ready.
var trustWindow = 10 * time.Second

// answerTrust watches a fresh pane for a folder-trust dialog and picks its
// "Yes, I trust …" option, so a tool fleet could not pre-mark still starts
// unattended. It runs before autostart: the dialog reads as waiting, so
// without it the first prompt would be pasted into the menu.
//
// It only watches a tool that is known to ask on screen and that fleet could
// not pre-mark — watching costs a spawn real seconds whenever a tool takes a
// while to draw its box, and that is a price for agy, not for every spawn.
// With auto_trust off it does nothing either: the user turned it off to
// answer by hand. It returns true when it answered a dialog.
func (f *Fleet) answerTrust(io paneIO, sleep func(time.Duration), pane string, p tools.Profile, pr prefs.Spawn) bool {
	log := logx.For("trust")
	set := f.Patterns[p.Patterns]
	if !pr.AutoTrust || !asksAboutTrust(set) {
		return false
	}
	ready := f.readyFunc(p)
	for waited := time.Duration(0); waited < trustWindow; waited += readyPoll {
		text, err := io.Capture(pane, 0)
		switch {
		case err != nil:
			return false
		case trustMenu.MatchString(text):
			m, ok := set.Menu(text)
			if !ok {
				log.Warn("trust dialog on screen but no menu to pick from", "pane", pane, "profile", p.Name)
				return false
			}
			yes := first(m.Options, "Yes")
			if yes == 0 {
				log.Warn("trust dialog has no yes option", "pane", pane, "options", m.Options)
				return false
			}
			if err := io.SendKeys(pane, MenuKeys(m.Selected, yes)...); err != nil {
				log.Warn("answering trust dialog failed", "pane", pane, "err", err)
				return false
			}
			log.Info("answered trust dialog on screen", "pane", pane, "profile", p.Name, "option", m.Options[yes-1])
			sleep(readySettle)
			return true
		case ready(text):
			return false // input box is up and no dialog came
		}
		sleep(readyPoll)
	}
	return false
}

// trustHint finds the trust question in a tool's own waiting patterns, which
// is how fleet knows a tool asks about folders at all. Patterns are data
// (DESIGN §15), so a new tool that asks needs no code here.
var trustHint = regexp.MustCompile(`(?i)(trust|quick safety check)`)

// asksAboutTrust reports whether this pattern set describes a tool that puts
// a folder-trust question on screen.
func asksAboutTrust(set status.Set) bool {
	for _, re := range set.Waiting {
		if trustHint.MatchString(re.String()) {
			return true
		}
	}
	return false
}
