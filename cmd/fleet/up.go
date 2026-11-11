package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/keys"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/sysx"
	"github.com/sturlabragason/fleet/internal/theme"
	"github.com/sturlabragason/fleet/internal/tools"
	"github.com/sturlabragason/fleet/internal/wall"
	"github.com/sturlabragason/fleet/internal/watch"
)

// supLaunch is how to start the admiral pane.
type supLaunch struct {
	Cmd, Dir string
	Env      []string
	ID       string // session id, "" if the profile can't set one
	Profile  string
	Paste    string // boot prompt to paste once ready (profile has no prompt_args)
}

// supervisorCmd builds the admiral command for profile ("": resolved by
// resolveAdmiral, DESIGN §6.10). A non-empty boot prompt goes through the
// profile's prompt_args (copilot -i, claude positional), else into Paste.
func supervisorCmd(f *fleet.Fleet, prof, boot string) (supLaunch, error) {
	return supervisorCmdOf(f, supervisor.Main, prof, boot)
}

// supervisorCmdOf is supervisorCmd for admiral adm.
func supervisorCmdOf(f *fleet.Fleet, adm, prof, boot string) (supLaunch, error) {
	if prof == "" {
		prof = resolveAdmiralOf(f, adm)
	}
	return admiralLaunchOf(f, adm, prof, "", boot)
}

// admiralLaunch builds the admiral command; a non-empty resumeID resumes that
// session with the profile's resume args instead of starting a new one.
func admiralLaunch(f *fleet.Fleet, prof, resumeID, boot string) (supLaunch, error) {
	return admiralLaunchOf(f, supervisor.Main, prof, resumeID, boot)
}

// admiralLaunchOf is admiralLaunch for admiral adm. Its MCP server learns who
// it serves from --admiral in its own config (claude: inline; copilot: the
// admiral's .mcp.json) and, for a non-main admiral, FLEET_ADMIRAL_NAME.
func admiralLaunchOf(f *fleet.Fleet, adm, prof, resumeID, boot string) (supLaunch, error) {
	p, ok := f.Profiles[prof]
	if !ok {
		return supLaunch{}, fmt.Errorf("admiral profile %q not found", prof)
	}
	if err := f.CheckCommand(prof, p, ""); err != nil {
		return supLaunch{}, err
	}
	p, _, err := fleet.AdmiralModel(prof, p)
	if err != nil {
		return supLaunch{}, err
	}
	// Every admiral starts in its own dir under fleet home, outside any git
	// checkout: Copilot puts the cwd's git state in its prompt, so each
	// merge in the repo cost a cold prompt-cache rewrite (ADR-93).
	dir := admiralEnv("DIR")
	if p.Patterns == "copilot" {
		// Its own .mcp.json is its only fleet server; without it the
		// admiral would start with no fleet tools at all, so fail instead.
		if _, err := writeAdmiralMCP(f.Home, f.Exe, adm); err != nil {
			return supLaunch{}, fmt.Errorf("admiral %s: write its MCP config: %w", adm, err)
		}
		if adm != supervisor.Main {
			dir = supervisor.DirOf(f.Home, adm)
		}
	}
	if p.Patterns == "claude" {
		if err := writeAdmiralRules(f.Home, adm); err != nil {
			return supLaunch{}, fmt.Errorf("admiral %s: write its rules: %w", adm, err)
		}
		if resumeID != "" {
			dir = claudeResumeDir(f, adm, resumeID, dir)
		}
	}
	if dir == "" {
		dir = supervisor.DirOf(f.Home, adm)
		os.MkdirAll(dir, 0o700)
	}
	if _, err := os.Stat(dir); err != nil {
		// No checkout next to the binary (go install): the admiral works
		// through MCP tools, not files, so any readable dir will do — but not
		// the home directory (ADR-47), which tools treat as a project.
		dir = filepath.Join(logx.Home(), fleet.ScratchRoot, "admiral")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			home, _ := os.UserHomeDir()
			logx.For("up").Warn("admiral scratch dir failed, using home", "err", err, "home", home)
			dir = home
		} else {
			logx.For("up").Info("admiral dir missing, using a scratch dir", "dir", dir)
		}
	}
	// The admiral is a tool too: pre-answer its trust dialog for its dir.
	f.TrustDir(prof, p, dir)
	id := resumeID
	if id == "" && len(p.LaunchID) > 0 {
		id = tools.NewUUID()
	}
	name := "admiral"
	if adm != supervisor.Main {
		name += "-" + adm
	}
	argv, embedded := p.Argv(tools.Vars{Fleet: f.Exe, Name: name, ID: id},
		tools.Launch{Resume: resumeID != "", Mode: admiralMode(p), Extra: admiralMCPArgsOf(p, f.Exe, f.Home, adm), Prompt: boot})
	logx.For("up").Info("admiral", "profile", prof, "dir", dir, "argv", argv, "resume", resumeID != "")
	sl := supLaunch{Cmd: tools.ShellCommand(argv), Dir: dir, Env: p.EnvList(), ID: id, Profile: prof}
	if p.Patterns == "claude" {
		sl.Env = append(sl.Env, claudeAdmiralEnv...)
	}
	if adm != supervisor.Main {
		sl.Env = append(sl.Env, AdmiralEnv+"="+adm)
	}
	if boot != "" && !embedded {
		sl.Paste = boot
	}
	return sl, nil
}

// claudeResumeDir is where a claude admiral resumes resumeID: Claude finds a
// session by its project dir, so the dir the session was saved with, or for a
// session saved before ADR-93 the old repo dir (FLEET_ADMIRAL_DIR if set).
func claudeResumeDir(f *fleet.Fleet, adm, resumeID, dir string) string {
	s, ok := supervisor.LoadOf(f.Home, adm)
	if !ok || s.SessionID != resumeID {
		return dir
	}
	if s.Dir != "" {
		if _, err := os.Stat(s.Dir); err == nil {
			return s.Dir
		}
		return dir
	}
	if dir != "" {
		return dir
	}
	return legacyAdmiralDir(f)
}

// legacyAdmiralDir is the admiral dir before ADR-93: supervisor/ in the
// checkout the binary was built in.
func legacyAdmiralDir(f *fleet.Fleet) string {
	return filepath.Join(filepath.Dir(filepath.Dir(f.Exe)), "supervisor")
}

// admiralMode starts the admiral in its tool's interactive mode when the
// profile names one (copilot: --mode interactive). An admiral idles while its
// agents work; in autopilot every idle turn ends in a "not complete yet"
// nudge that buys a full-context turn (ADR-70).
func admiralMode(p tools.Profile) string {
	if _, ok := p.ModeArgs["interactive"]; ok {
		return "interactive"
	}
	return ""
}

// supervisorMCPArgs makes a copilot admiral lean: only the fleet MCP tools
// (loaded from supervisor/.mcp.json) are available and pre-approved, and the
// global instructions and skills are skipped. The admiral rules arrive as
// the fleet MCP server instructions. Measured: ~8k input tokens per turn
// instead of ~160k, and no reading of Go code or shelling out.
func supervisorMCPArgs(p tools.Profile) []string {
	if p.Patterns != "copilot" {
		return nil
	}
	return append([]string{
		"--no-custom-instructions",
		"--allow-all-mcp-server-instructions",
		"--available-tools", "fleet",
		"--allow-tool", "fleet",
		"--disable-builtin-mcps",
	}, disableUserMCPArgs(userMCPConfig())...)
}

func userMCPConfig() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".copilot", "mcp-config.json")
}

// disableUserMCPArgs disables the user's own MCP servers for the admiral so
// their tools don't load (and don't flood the startup "Disabled tools" notice).
// Workers get the same through the profile's user_mcp_off (ADR-91).
func disableUserMCPArgs(path string) []string {
	var args []string
	for _, n := range tools.UserMCPNames(tools.UserMCPServers(path), nil) {
		args = append(args, "--disable-mcp-server", n)
	}
	return args
}

func cmdUp(f *fleet.Fleet, args []string) error {
	t := f.T
	log := logx.For("up")
	noAttach, explicit, monitors, err := parseUpArgs(args)
	if err != nil {
		return err
	}
	prof, warns, err := resolveAdmiralProfile(explicit, f.Profiles, os.Getenv, supervisor.LoadChoice(f.Home), f.Prefs.AdmiralProfile())
	if err != nil {
		return err
	}
	target := fleet.Session + ":main"
	if _, err := t.Run("has-session", "-t", "="+fleet.Session); err != nil || !windowExists(f, "main") {
		sl, err := supervisorCmd(f, prof, "")
		if err != nil {
			return err
		}
		var a []string
		if t.HasSession(fleet.Session) {
			a = []string{"new-window", "-d", "-t", fleet.Session + ":", "-n", "main"}
		} else {
			a = []string{"new-session", "-d", "-s", fleet.Session, "-n", "main", "-x", "300", "-y", "80"}
		}
		a = append(a, "-P", "-F", "#{pane_id}", "-c", sl.Dir)
		for _, e := range sl.Env {
			a = append(a, "-e", e)
		}
		sup, err := t.Run(append(a, sl.Cmd)...)
		if err != nil {
			return err
		}
		t.SetPaneOption(sup, "@role", "sup")
		t.SetPaneOption(sup, "remain-on-exit", "on")
		t.SetPaneOption(sup, "@fleet_admiral_profile", sl.Profile)
		if explicit != "" {
			saveChoice(f, explicit)
		}
		if err := supervisor.Save(f.Home, supervisor.Session{SessionID: sl.ID, Profile: sl.Profile, PaneID: sup, Started: time.Now(), Dir: sl.Dir, Generation: 1}); err != nil {
			log.Error("save admiral session", "err", err)
		}
		log.Info("created main window", "sup", sup)
	} else if explicit != "" {
		upSwap(f, explicit)
	}
	ensureAdmirals(f)
	retheme(f)
	ensureOverview(f, target)
	configure(f)
	// One watcher per fleet keeps status fresh whatever is open (ADR-49).
	startWatcher(f)
	warnAdmiral(f, warns)
	switch {
	case monitors == 2:
		if err := splitMonitors(f); err != nil {
			log.Error("two monitors", "err", err)
		}
	case monitors == 1 && twoMonitors(f):
		if err := joinMonitors(f); err != nil {
			log.Error("one monitor", "err", err)
		}
	default:
		if err := wall.New(t, fleet.Session).Apply(); err != nil {
			log.Error("apply", "err", err)
		}
	}
	attach := term.IsTerminal(int(os.Stdin.Fd())) && !noAttach
	if twoMonitors(f) {
		if w := admiralTarget(f); w != "" {
			target = fleet.Session + ":" + w
		}
		if monitors == 2 {
			if !attach {
				fmt.Println("fleet is up on two monitors.\n  monitor 1: tmux -L fleet attach -t fleet:" + admiralWindow + "\n  monitor 2: fleet wall attach")
				return nil
			}
			if line, err := openWallTerminal(f); err != nil {
				fmt.Fprintf(os.Stderr, "fleet: could not open the wall's terminal: %v\nrun `fleet wall attach` in a second terminal\n", err)
			} else {
				log.Info("wall terminal", "cmd", line)
			}
		}
	}
	if !attach {
		fmt.Println("fleet is up. attach with: tmux -L fleet attach -t fleet:main")
		return nil
	}
	t.Run("select-window", "-t", target)
	return attachTmux("attach-session", "-t", target)
}

func windowExists(f *fleet.Fleet, name string) bool {
	out, _ := f.T.Run("list-windows", "-t", fleet.Session, "-F", "#{window_name}")
	for _, l := range strings.Split(out, "\n") {
		if l == name {
			return true
		}
	}
	return false
}

// retheme makes a theme change take effect on `fleet up` without touching a
// single agent (ADR-55). The global chrome is re-set by configure and the
// overview pane is respawned by ensureOverview, but two things cache the old
// palette: the agent pane borders, whose colour is a pane option written on a
// status transition, and the watcher process, which was started before the
// change and would write the old hex back. So the stale status tags are
// dropped — the next refresh re-tags every pane from the new palette — and the
// watcher is stopped by its own pid, for startWatcher to start again.
func retheme(f *fleet.Fleet) { rethemeWith(f, true) }

// rethemeWith is retheme; stopWatcher false is the watcher itself applying
// the palette it just loaded, so there is no stale watcher to stop.
func rethemeWith(f *fleet.Fleet, stopWatcher bool) {
	name := theme.Active().Name
	cur, _ := f.T.Run("show-options", "-qv", "-t", fleet.Session, "@fleet_theme")
	if strings.TrimSpace(cur) == name {
		return
	}
	log := logx.For("theme")
	log.Info("palette changed", "theme", name)
	panes, _ := f.T.ListPanes("")
	for _, p := range panes {
		if p.Agent != "" && p.Status != "" {
			f.T.UnsetPaneOption(p.ID, "@fleet_status")
		}
	}
	if pid, ok := watch.Running(f.Home); stopWatcher && ok {
		if err := sysx.Kill(pid, syscall.SIGTERM); err != nil {
			log.Error("stop watcher for the new palette", "pid", pid, "err", err)
		} else {
			waitWatcherGone(f.Home, pid, 5*time.Second)
		}
	}
	f.T.Run("set-option", "-t", fleet.Session, "@fleet_theme", name)
}

func ensureOverview(f *fleet.Fleet, target string) {
	t := f.T
	panes, _ := t.ListPanes(target)
	uiCmd := tools.ShellCommand([]string{f.Exe, "ui"})
	for _, p := range panes {
		if p.Role == "ov" {
			if p.Dead || t.PaneOption(p.ID, "@fleet_theme") != theme.Active().Name {
				t.Run("respawn-pane", "-k", "-t", p.ID, uiCmd)
				t.SetPaneOption(p.ID, "@fleet_theme", theme.Active().Name)
			}
			return
		}
	}
	for _, p := range panes {
		if p.Role == "sup" && p.Dead {
			t.Run("respawn-pane", "-t", p.ID)
			t.UnsetPaneOption(p.ID, "@fleet_exit_told")
		}
	}
	ov, err := t.Run("split-window", "-d", "-h", "-t", target, "-P", "-F", "#{pane_id}", uiCmd)
	if err != nil {
		return
	}
	t.SetPaneOption(ov, "@role", "ov")
	t.SetPaneOption(ov, "remain-on-exit", "on")
	t.SetPaneOption(ov, "@fleet_theme", theme.Active().Name)
}

// configure sets server options, key bindings, hooks and border polish, and
// records which set it applied in chromeOption, for hotApply.
func configure(f *fleet.Fleet) {
	cmds := tmuxCommands(f)
	for _, a := range cmds {
		f.T.Run(a...)
	}
	f.T.Run("set-option", "-g", chromeOption, chromeStamp(cmds))
}

// chromeOption holds the stamp of the tmux commands configure last ran.
const chromeOption = "@fleet_chrome"

// chromeStamp identifies a set of tmux commands: a release that changes an
// option, hook or binding changes it.
func chromeStamp(cmds [][]string) string {
	h := sha256.New()
	for _, a := range cmds {
		for _, s := range a {
			h.Write([]byte(s))
			h.Write([]byte{0})
		}
		h.Write([]byte{1})
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// hotApply brings a running fleet's tmux server up to this binary without
// `fleet up` (ADR-111): the watcher runs it each time it starts, which
// includes re-exec'ing into a new release (ADR-62). Every command configure
// runs is idempotent, so it only runs when the stamp differs. It reports
// whether it re-applied anything.
func hotApply(f *fleet.Fleet) bool {
	if !f.T.HasSession(fleet.Session) {
		return false
	}
	rethemeWith(f, false)
	cmds := tmuxCommands(f)
	cur, _ := f.T.Run("show-options", "-gqv", chromeOption)
	if strings.TrimSpace(cur) == chromeStamp(cmds) {
		return false
	}
	configure(f)
	logx.For("watch").Info("re-applied tmux options for this build", "commands", len(cmds))
	return true
}

// tmuxCommands returns the tmux commands configure runs. Pure, so tests can
// check the key bindings against the keys registry without tmux.
func tmuxCommands(f *fleet.Fleet) [][]string {
	exe := tools.Quote(f.Exe)
	wallCmd := func(a string) string { return "run-shell -b '" + f.Exe + " wall " + a + "'" }
	set := [][]string{
		{"set-option", "-g", "mouse", "on"},
		{"set-option", "-s", "escape-time", "10"},
		// Synchronized output: tmux paints each update in one go, so
		// VTE/Ptyxis (whose TERM tmux does not know has it) stops showing
		// half-drawn frames and a cursor that jumps out of the active pane
		// on every overview repaint. Terminals without it ignore the mode.
		// A fixed index, not -a: `fleet up` runs again on every attach.
		{"set-option", "-s", "terminal-features[90]", "xterm*:sync"},
		// OSC 8 hyperlinks: without this tmux keeps the link in the pane
		// but sends the outer terminal plain text, so a URL wrapped across
		// rows is clickable only up to the line break.
		{"set-option", "-s", "terminal-features[91]", "xterm*:hyperlinks"},
		{"set-option", "-g", "pane-border-status", "top"},
		{"set-option", "-g", "pane-border-lines", "heavy"},
		{"set-option", "-g", "pane-border-style", "fg=" + theme.Surface1},
		// Not Select: Lavender means "target slot" and nothing else (ADR-37).
		{"set-option", "-g", "pane-active-border-style", "fg=" + theme.Text},
		{"set-option", "-g", "pane-border-format",
			" #{?#{==:#{@role},sup},#[fg=#{?" + fleet.MultiOption + ",#{@fleet_admiral_color}," + theme.Supervisor + "}]#[bold]admiral#{?" + fleet.MultiOption + ", #{@fleet_admiral_name},}#[default]#{?@fleet_admiral_profile,#[fg=" + theme.Muted + "] · #{@fleet_admiral_profile}#[default],}#{?" + fleet.MultiOption + ",#{?@fleet_admiral_unread,#[fg=" + theme.Waiting + "] · ● #{@fleet_admiral_unread}#[default],},}#{?@fleet_ctx,#[fg=#{@fleet_ctx_color}] · ctx #{@fleet_ctx}#[default],},#{?#{==:#{@role},ov},#[fg=" + theme.Text + "]#[bold]overview#[default]," + targetMark + ownerMark + "#[fg=#{?@fleet_color,#{@fleet_color},default}]#{@fleet_glyph} #{@fleet_agent}" + pinMark + noReportMark + "#{?@fleet_team, #[fg=#{@fleet_team_color}]◆,} #[fg=" + theme.Muted + "]#{@fleet_status}#[default]" + nextOutMark + "}} "},
		// Unfocused panes recede one step (Overlay2, 5.8:1 on Base: still
		// readable, so an emphasised pane you are not in stays legible).
		{"set-option", "-g", "window-style", "fg=" + theme.Overlay2 + ",bg=" + theme.Base},
		{"set-option", "-g", "window-active-style", "fg=" + theme.Text + ",bg=" + theme.Base},
		{"set-option", "-g", "status-style", "fg=" + theme.Text + ",bg=" + theme.Mantle},
		{"set-option", "-g", "message-style", "fg=" + theme.Text + ",bg=" + theme.Surface0},
		{"set-option", "-g", "message-command-style", "fg=" + theme.Text + ",bg=" + theme.Surface0},
		{"set-option", "-g", "mode-style", "fg=" + theme.Base + ",bg=" + theme.Select},
		{"set-option", "-g", "copy-mode-match-style", "fg=" + theme.Base + ",bg=" + theme.Waiting},
		{"set-option", "-g", "copy-mode-current-match-style", "fg=" + theme.Base + ",bg=" + theme.WaitingAlt},
		{"set-option", "-g", "display-panes-colour", theme.Overlay0},
		{"set-option", "-g", "display-panes-active-colour", theme.Text},
		{"set-option", "-g", "clock-mode-colour", theme.Supervisor},
		// The unread-report badge (ADR-36), a steady ● in the waiting colour (ADR-81), is set by the resident poll: the watcher when live (ADR-75), else the overview.
		{"set-option", "-g", "status-left", "#[fg=" + theme.Crust + ",bg=" + theme.Supervisor + ",bold] fleet #[default] #{?" + fleet.BadgeOption + ",#[fg=" + theme.Waiting + "]#{?#{&&:#{" + fleet.MultiOption + "},#{" + fleet.BadgeByOption + "}},#{" + fleet.BadgeByOption + "}, ● #{" + fleet.BadgeOption + "} }#[default] ,}" + usageSegment()},
		{"set-option", "-g", "status-left-length", "150"},
		{"set-option", "-g", "window-status-separator", ""},
		{"set-option", "-g", "window-status-format", "#[fg=" + theme.Overlay1 + "] #I #W "},
		{"set-option", "-g", "window-status-current-format", "#[fg=" + theme.Crust + ",bg=" + theme.Select + ",bold] #I #W #[default]"},
		{"set-option", "-g", "status-right", "#[fg=" + theme.Overlay1 + "]" + clickableChanges(keys.Hint(keys.ScopeTmux, statusRightLen-1)) + " "},
		{"set-option", "-g", "status-right-length", strconv.Itoa(statusRightLen)},
		{"set-hook", "-g", "client-resized", wallCmd("apply")},
		{"set-hook", "-g", "client-attached", wallCmd("apply")},
		// Focusing a wall pane makes it the target slot (DESIGN.md §5e), inline
		// so no process runs per click; the overview and admiral leave it alone.
		// The refresh fires on every pane switch, so it takes the cached path:
		// an unchanged layout string costs one tmux round trip.
		{"set-hook", "-g", "pane-focus-in", targetHook + " ; " + wallCmd("refresh")},
		{"bind-key", "-n", "M-Left", "select-pane", "-L"},
		{"bind-key", "-n", "M-Right", "select-pane", "-R"},
		{"bind-key", "-n", "M-Up", "select-pane", "-U"},
		{"bind-key", "-n", "M-Down", "select-pane", "-D"},
		{"bind-key", "-n", "M-n", wallCmd("--notify next")},
		{"bind-key", "-n", "M-z", "resize-pane", "-Z"},
		{"bind-key", "-n", "M-x", wallCmd("hide-focused")},
		{"bind-key", "-n", "M-X", wallCmd("--notify toggle-all")},
		{"bind-key", "-n", "M-s", wallCmd("sup")},
		{"bind-key", "-n", "M-S", wallCmd("side")},
		{"bind-key", "-n", "M-c", wallCmd("chips")},
		{"bind-key", "-n", "M-o", "select-pane", "-t", "{marked}"},
		// M-R and M-P act on the focused admiral, or ask which (DESIGN §6.12).
		{"bind-key", "-n", "M-R", wallCmdAct(f.Exe, "reset")},
		{"bind-key", "-n", "M-P", wallCmdAct(f.Exe, "pick")},
		{"bind-key", "-n", keys.HelpTmuxKey, "display-popup", "-E", "-b", "rounded",
			"-w", strconv.Itoa(keys.PopupWidth()), "-h", strconv.Itoa(keys.PopupHeight()),
			"-T", " fleet keys · any key closes ", exe + " keys --popup"},
		append([]string{"bind-key", "-n", keys.ChangesTmuxKey}, changesPopupArgs(exe)...),
		changesStatusClick(exe),
	}
	for _, p := range []string{"1", "2", "4", "6", "9", "m"} {
		set = append(set, []string{"bind-key", "-n", "M-" + p, wallCmd("preset " + p)})
		// Chord for terminals that grab Alt+digit (Ptyxis/GNOME use it for tabs).
		set = append(set, []string{"bind-key", "-T", "fleetwall", p, wallCmd("preset " + p)})
	}
	// These drags move panes out of the wall order or make untracked panes.
	for _, k := range []string{"M-MouseDrag1Pane", "M-MouseDrag1Border", "C-MouseDrag1Pane"} {
		set = append(set, []string{"unbind-key", "-n", k})
	}
	set = append(set, []string{"bind-key", "-n", "M-w", "switch-client", "-T", "fleetwall", "\\;", "display-message", "wall layout: press 1 2 4 6 9 or m (release Alt first)"})
	set = append(set, mouseSelectBindings()...)
	if cc := hostCopyCommand(); cc != "" {
		set = append(set, []string{"set-option", "-s", "copy-command", cc})
	}
	return set
}

// mouseSelectBindings makes a left-button drag select text for the clipboard
// in every agent pane (ADR-38). tmux's defaults hand the drag to the pane's
// program whenever it asked for the mouse (`#{mouse_any_flag}`), and every
// fleet pane runs such a program: Copilot, Claude and the overview TUI all
// turn mouse reporting on. With `mouse on` the terminal does not make a
// native selection either, so a drag-select produced nothing to copy and
// ADR-28's copy-command never ran. Dropping the mouse_any_flag test gives the
// drag to tmux; clicks, the wheel and the right-click menu still reach the
// program, which is what the TUIs use.
func mouseSelectBindings() [][]string {
	// Selecting in copy-mode already works, so there the drag keeps going to
	// the mode (send-keys -M); the copy-mode table's MouseDragEnd1Pane runs
	// copy-pipe-and-cancel, which pipes through copy-command. The overview is
	// the other exception: it acts on the left-button release (click a card,
	// ADR-31), and tmux swallows the release once it takes the drag, so a
	// click that slips one cell would do nothing. It holds no text worth
	// copying, so it keeps tmux's old behaviour.
	keep := "if-shell -F '#{||:#{pane_in_mode},#{==:#{@role},ov}}' 'send-keys -M' "
	// run-shell -d 0.3 leaves the highlight on screen long enough to see.
	grab := func(selection string) string {
		return keep + "'copy-mode -H ; send-keys -X " + selection +
			" ; run-shell -d 0.3 ; send-keys -X copy-pipe-and-cancel'"
	}
	return [][]string{
		{"bind-key", "-n", "MouseDrag1Pane", keep + "'copy-mode -M'"},
		{"bind-key", "-n", "DoubleClick1Pane", "select-pane -t = ; " + grab("select-word")},
		{"bind-key", "-n", "TripleClick1Pane", "select-pane -t = ; " + grab("select-line")},
	}
}

// mouseSelectKeys are the keys mouseSelectBindings rebinds.
var mouseSelectKeys = []string{"MouseDrag1Pane", "DoubleClick1Pane", "TripleClick1Pane"}

const statusRightLen = 120 // fits the full tmux hint (keys.Hint ScopeTmux) plus a space

// usageSegment is the usage bar after the badge (ADR-97): today's spend per
// profile and the Copilot month share, set by the badge writer. A steady
// colour marks 80% (waiting) and 100% (error) of a known allowance.
func usageSegment() string {
	return "#{?" + fleet.UsageOption + ",#[fg=#{?#{==:#{" + fleet.UsageLevelOption + "},over}," + theme.Error + ",#{?#{==:#{" + fleet.UsageLevelOption + "},warn}," + theme.Waiting + "," + theme.Overlay1 + "}}]#{" + fleet.UsageOption + "}#[default] ,}"
}

// targetHook runs on pane-focus-in: a focused wall pane becomes the target
// slot (@fleet_target on its window).
const targetHook = `if-shell -F '#{==:#{@role},wall}' 'set-option -wF @fleet_target "#{pane_id}"'`

// The border-title marks carry palette colours, so they are rebuilt whenever
// the active palette changes (ADR-55).
var (
	// targetMark starts the border title of the target pane with a bold ▶ in
	// the select colour; the agent colour that follows still applies.
	targetMark string
	// pinMark follows the agent name on a pinned pane's border (@fleet_pin,
	// DESIGN.md §5e): ⚑ in Overlay1, then the name colour again.
	pinMark string
	// noReportMark follows the pin on the border of an agent that went idle
	// mid-task without a report (@fleet_noreport, ADR-68): … in Waiting, or
	// muted when it is the user's agent ("user").
	noReportMark string
	// nextOutMark ends the title of the pane a card would evict (no target,
	// full wall).
	nextOutMark string
	// ownerMark is the owner chip on an agent's border while several
	// admirals exist (@fleet_owner is only set then, DESIGN §6.12).
	ownerMark string
)

func init() {
	theme.OnChange(func() {
		targetMark = "#{?#{==:#{pane_id},#{@fleet_target}},#[fg=" + theme.Select + "]#[bold]▶#[nobold] ,}"
		noReportMark = "#{?#{==:#{@fleet_noreport},1}, #[fg=" + theme.Waiting + "]…#[fg=#{?@fleet_color,#{@fleet_color},default}],}" +
			"#{?#{==:#{@fleet_noreport},user}, #[fg=" + theme.Muted + "]…#[fg=#{?@fleet_color,#{@fleet_color},default}],}"
		pinMark = "#{?@fleet_pin, #[fg=" + theme.Overlay1 + "]⚑#[fg=#{?@fleet_color,#{@fleet_color},default}],}"
		ownerMark = "#{?" + fleet.MultiOption + ",#{?@fleet_owner,#[fg=" + theme.Crust + "#,bg=#{@fleet_owner_color}#,bold] #{@fleet_owner} #[default] ,},}"
		nextOutMark = "#{?#{==:#{pane_id},#{@fleet_next_out}}, #[fg=" + theme.Gone + "]· next out#[default],}"
	})
}

// wallCmdAct is the key binding that runs `fleet admiral act VERB` for the
// focused pane.
func wallCmdAct(exe, verb string) string {
	return "run-shell -b " + tools.Quote(tools.Quote(exe)+" admiral act "+verb+` --pane "#{pane_id}"`)
}

// wallUsage lists the `fleet wall` verbs.
const wallUsage = "fleet wall attach|apply|refresh|preset N|sup|side|chips|emphasis|show NAME|hide NAME|add NAME|next|prev|untarget|undo|pin NAME|hide-all|restore|toggle-all [--keep-focus] [--notify]"

// pinMsg says what a pin toggle did (the overview's p says the same).
func pinMsg(name string, on bool) string {
	if on {
		return name + " pinned ⚑"
	}
	return name + " unpinned"
}

func cmdWall(f *fleet.Fleet, args []string) error {
	w := wall.New(f.T, fleet.Session)
	// --keep-focus (the right-click menu): show and add leave keyboard focus
	// where it is, as from the overview. --notify (also the menu): the result
	// line, or the error, goes to a tmux message instead of stdout/stderr.
	notify := false
	rest := args[:0:0]
	for _, a := range args {
		switch a {
		case "--keep-focus":
			w.KeepFocus = true
		case "--notify":
			notify = true
		default:
			rest = append(rest, a)
		}
	}
	args = rest
	if len(args) == 0 {
		return fmt.Errorf("usage: %s", wallUsage)
	}
	if args[0] == "attach" {
		return wallAttach(f)
	}
	say := func(msg string) {
		if msg == "" {
			return
		}
		if notify {
			f.T.Run("display-message", "-d", "3000", "-t", w.Target, msg)
			return
		}
		fmt.Println(msg)
	}
	msg, err := wallVerb(f, w, args)
	if err != nil && notify {
		say("wall " + args[0] + ": " + err.Error())
		return nil
	}
	say(msg)
	return err
}

// wallVerb runs one `fleet wall` verb and returns its result line ("" for
// the verbs the tmux key bindings run silently).
func wallVerb(f *fleet.Fleet, w *wall.Wall, args []string) (string, error) {
	named := func() (name, pane string, err error) {
		if len(args) < 2 {
			return "", "", fmt.Errorf("usage: fleet wall %s NAME", args[0])
		}
		pane, err = f.Pane(args[1])
		return args[1], pane, err
	}
	// show and add wake a parked agent first (#45); woken prefixes the result.
	woken := ""
	waking := func() (name, pane string, err error) {
		if len(args) < 2 {
			return "", "", fmt.Errorf("usage: fleet wall %s NAME", args[0])
		}
		pane, woke, err := f.PaneOrWake(args[1])
		if woke {
			woken = "woke " + args[1] + "; "
		}
		return args[1], pane, err
	}
	switch args[0] {
	case "apply":
		return "", w.Apply()
	case "refresh":
		// Cached: used by the pane-focus-in hook, which fires constantly.
		return "", w.ApplyCached()
	case "preset":
		if len(args) < 2 {
			return "", fmt.Errorf("usage: fleet wall preset 1|2|4|6|9|m")
		}
		return "", w.SetPreset(args[1])
	case "sup":
		return "", w.ToggleSup()
	case "side":
		return "", w.SwapSide()
	case "chips":
		return "", w.ToggleChips()
	case "emphasis":
		return "", w.ToggleEmphasis()
	case "hide-focused":
		return "", w.HideFocused()
	case "untarget":
		return "", w.Untarget()
	case "hide-all":
		n, err := w.HideAll()
		return wall.HiddenAllMsg(n), err
	case "restore":
		n, err := w.Restore()
		return wall.RestoredMsg(n), err
	case "toggle-all":
		return w.ToggleAll()
	case "undo":
		msg, _, err := w.Undo()
		return msg, err
	case "pin":
		name, p, err := named()
		if err != nil {
			return "", err
		}
		on, err := w.TogglePin(p)
		if err != nil {
			return "", err
		}
		return pinMsg(name, on), nil
	case "next", "prev":
		dir := 1
		if args[0] == "prev" {
			dir = -1
		}
		name, ok, err := w.Next(dir)
		switch {
		case err != nil:
			return "", err
		case name == "":
			return wall.NothingWaitingMsg, nil
		case !ok:
			return wall.FullMsg, nil
		}
		return "", nil
	case "add":
		name, p, err := waking()
		if err != nil {
			return "", err
		}
		wasOn := w.OnWall()[name]
		ok, err := w.Add(p)
		switch {
		case err != nil:
			return "", err
		case !ok:
			return woken + wall.AddFullMsg, nil
		case wasOn:
			return woken + name + " on wall", nil
		}
		return woken + name + " added to wall", nil
	case "hide":
		name, p, err := named()
		if err != nil {
			return "", err
		}
		return name + " off wall", w.Hide(p)
	case "show":
		name, p, err := waking()
		if err != nil {
			return "", err
		}
		before, wasOn := w.TargetAgent(), w.OnWall()[name]
		ok, err := w.Show(p)
		switch {
		case err != nil:
			return "", err
		case !ok:
			// display-message goes nowhere from a plain shell, so say it here too.
			return woken + wall.FullMsg, nil
		}
		return woken + wall.ShownMsg(name, before, wasOn, before != "" && w.OnWall()[before]), nil
	}
	return "", fmt.Errorf("unknown wall command %q", args[0])
}
