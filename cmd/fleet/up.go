package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/keys"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/theme"
	"github.com/sturlabragason/fleet/internal/tools"
	"github.com/sturlabragason/fleet/internal/wall"
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
	if prof == "" {
		prof = resolveAdmiral(f)
	}
	return admiralLaunch(f, prof, "", boot)
}

// admiralLaunch builds the admiral command; a non-empty resumeID resumes that
// session with the profile's resume args instead of starting a new one.
func admiralLaunch(f *fleet.Fleet, prof, resumeID, boot string) (supLaunch, error) {
	p, ok := f.Profiles[prof]
	if !ok {
		return supLaunch{}, fmt.Errorf("admiral profile %q not found", prof)
	}
	dir := admiralEnv("DIR")
	if dir == "" {
		dir = filepath.Join(filepath.Dir(filepath.Dir(f.Exe)), "supervisor")
	}
	if _, err := os.Stat(dir); err != nil {
		// No checkout next to the binary (go install): any readable cwd will do,
		// the admiral works through MCP tools, not files.
		home, _ := os.UserHomeDir()
		logx.For("up").Info("admiral dir missing, using home", "dir", dir, "home", home)
		dir = home
	}
	id := resumeID
	if id == "" && len(p.LaunchID) > 0 {
		id = tools.NewUUID()
	}
	argv, embedded := p.Argv(tools.Vars{Fleet: f.Exe, Name: "admiral", ID: id},
		tools.Launch{Resume: resumeID != "", Extra: admiralMCPArgs(p, f.Exe), Prompt: boot})
	logx.For("up").Info("admiral", "profile", prof, "dir", dir, "argv", argv, "resume", resumeID != "")
	sl := supLaunch{Cmd: tools.ShellCommand(argv), Dir: dir, Env: p.EnvList(), ID: id, Profile: prof}
	if boot != "" && !embedded {
		sl.Paste = boot
	}
	return sl, nil
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
func disableUserMCPArgs(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return nil
	}
	names := make([]string, 0, len(cfg.MCPServers))
	for n := range cfg.MCPServers {
		if n != "fleet" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var args []string
	for _, n := range names {
		args = append(args, "--disable-mcp-server", n)
	}
	return args
}

func cmdUp(f *fleet.Fleet, args []string) error {
	t := f.T
	log := logx.For("up")
	noAttach, explicit, err := parseUpArgs(args)
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
		if err := supervisor.Save(f.Home, supervisor.Session{SessionID: sl.ID, Profile: sl.Profile, PaneID: sup, Started: time.Now(), Generation: 1}); err != nil {
			log.Error("save admiral session", "err", err)
		}
		log.Info("created main window", "sup", sup)
	} else if explicit != "" {
		upSwap(f, explicit)
	}
	ensureOverview(f, target)
	configure(f)
	warnAdmiral(f, warns)
	w := wall.New(t, fleet.Session)
	if err := w.Apply(); err != nil {
		log.Error("apply", "err", err)
	}
	if noAttach || !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Println("fleet is up. attach with: tmux -L fleet attach -t fleet:main")
		return nil
	}
	t.Run("select-window", "-t", target)
	tm, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	env := []string{}
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "TMUX=") {
			env = append(env, e)
		}
	}
	return syscall.Exec(tm, []string{"tmux", "-L", fleet.Socket, "attach-session", "-t", target}, env)
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

func ensureOverview(f *fleet.Fleet, target string) {
	t := f.T
	panes, _ := t.ListPanes(target)
	uiCmd := tools.ShellCommand([]string{f.Exe, "ui"})
	for _, p := range panes {
		if p.Role == "ov" {
			if p.Dead {
				t.Run("respawn-pane", "-k", "-t", p.ID, uiCmd)
			}
			return
		}
	}
	for _, p := range panes {
		if p.Role == "sup" && p.Dead {
			t.Run("respawn-pane", "-t", p.ID)
		}
	}
	ov, err := t.Run("split-window", "-d", "-h", "-t", target, "-P", "-F", "#{pane_id}", uiCmd)
	if err != nil {
		return
	}
	t.SetPaneOption(ov, "@role", "ov")
	t.SetPaneOption(ov, "remain-on-exit", "on")
}

// configure sets server options, key bindings, hooks and border polish.
func configure(f *fleet.Fleet) {
	for _, a := range tmuxCommands(f) {
		f.T.Run(a...)
	}
}

// tmuxCommands returns the tmux commands configure runs. Pure, so tests can
// check the key bindings against the keys registry without tmux.
func tmuxCommands(f *fleet.Fleet) [][]string {
	exe := tools.Quote(f.Exe)
	wallCmd := func(a string) string { return "run-shell -b '" + f.Exe + " wall " + a + "'" }
	set := [][]string{
		{"set-option", "-g", "mouse", "on"},
		{"set-option", "-s", "escape-time", "10"},
		{"set-option", "-g", "pane-border-status", "top"},
		{"set-option", "-g", "pane-border-lines", "heavy"},
		{"set-option", "-g", "pane-border-style", "fg=" + theme.Surface1},
		// Not Select: Lavender means "target slot" and nothing else (ADR-37).
		{"set-option", "-g", "pane-active-border-style", "fg=" + theme.Text},
		{"set-option", "-g", "pane-border-format",
			" #{?#{==:#{@role},sup},#[fg=" + theme.Supervisor + "]#[bold]admiral#[default]#{?@fleet_admiral_profile,#[fg=" + theme.Muted + "] · #{@fleet_admiral_profile}#[default],}#{?@fleet_ctx,#[fg=#{@fleet_ctx_color}] · ctx #{@fleet_ctx}#[default],},#{?#{==:#{@role},ov},#[fg=" + theme.Text + "]#[bold]overview#[default]," + targetMark + "#[fg=#{?@fleet_color,#{@fleet_color},default}]#{@fleet_glyph} #{@fleet_agent}" + pinMark + "#{?@fleet_team, #[fg=#{@fleet_team_color}]◆,} #[fg=" + theme.Muted + "]#{@fleet_status}#[default]" + nextOutMark + "}} "},
		// Unfocused panes recede one step (Overlay2, 5.8:1 on Base: still
		// readable, so an emphasised pane you are not in stays legible).
		{"set-option", "-g", "window-style", "fg=" + theme.Overlay2 + ",bg=" + theme.Base},
		{"set-option", "-g", "window-active-style", "fg=" + theme.Text + ",bg=" + theme.Base},
		{"set-option", "-g", "status-style", "fg=" + theme.Text + ",bg=" + theme.Mantle},
		{"set-option", "-g", "message-style", "fg=" + theme.Text + ",bg=" + theme.Surface0},
		{"set-option", "-g", "message-command-style", "fg=" + theme.Text + ",bg=" + theme.Surface0},
		{"set-option", "-g", "mode-style", "fg=" + theme.Base + ",bg=" + theme.Lavender},
		{"set-option", "-g", "copy-mode-match-style", "fg=" + theme.Base + ",bg=" + theme.Yellow},
		{"set-option", "-g", "copy-mode-current-match-style", "fg=" + theme.Base + ",bg=" + theme.Peach},
		{"set-option", "-g", "display-panes-colour", theme.Overlay0},
		{"set-option", "-g", "display-panes-active-colour", theme.Text},
		{"set-option", "-g", "clock-mode-colour", theme.Mauve},
		// The unread-report badge (ADR-36) is set by the overview poll.
		{"set-option", "-g", "status-left", "#[fg=" + theme.Crust + ",bg=" + theme.Mauve + ",bold] fleet #[default] #{?" + fleet.BadgeOption + ",#[fg=" + theme.Crust + "#,bg=" + theme.Waiting + "#,bold] ✉ #{" + fleet.BadgeOption + "} #[default] ,}"},
		{"set-option", "-g", "status-left-length", "70"},
		{"set-option", "-g", "window-status-separator", ""},
		{"set-option", "-g", "window-status-format", "#[fg=" + theme.Overlay1 + "] #I #W "},
		{"set-option", "-g", "window-status-current-format", "#[fg=" + theme.Crust + ",bg=" + theme.Lavender + ",bold] #I #W #[default]"},
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
		{"bind-key", "-n", "M-s", wallCmd("sup")},
		{"bind-key", "-n", "M-S", wallCmd("side")},
		{"bind-key", "-n", "M-c", wallCmd("chips")},
		{"bind-key", "-n", "M-o", "select-pane", "-t", "{marked}"},
		append([]string{"bind-key", "-n", "M-R"}, confirmResetArgs(f.Exe)...),
		append([]string{"bind-key", "-n", "M-P"}, pickPopupArgs(f)...),
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

// targetHook runs on pane-focus-in: a focused wall pane becomes the target
// slot (@fleet_target on its window).
const targetHook = `if-shell -F '#{==:#{@role},wall}' 'set-option -wF @fleet_target "#{pane_id}"'`

// targetMark starts the border title of the target pane with a bold ▶ in the
// select colour; the agent colour that follows still applies.
var targetMark = "#{?#{==:#{pane_id},#{@fleet_target}},#[fg=" + theme.Select + "]#[bold]▶#[nobold] ,}"

// nextOutMark ends the title of the pane a card would evict (no target, full wall).
// pinMark follows the agent name on a pinned pane's border (@fleet_pin, DESIGN.md
// §5e): ⚑ in Overlay1, then the name colour again.
var pinMark = "#{?@fleet_pin, #[fg=" + theme.Overlay1 + "]⚑#[fg=#{?@fleet_color,#{@fleet_color},default}],}"

var nextOutMark = "#{?#{==:#{pane_id},#{@fleet_next_out}}, #[fg=" + theme.Gone + "]· next out#[default],}"

// wallUsage lists the `fleet wall` verbs.
const wallUsage = "fleet wall apply|refresh|preset N|sup|side|chips|emphasis|show NAME|hide NAME|add NAME|next|prev|untarget|undo|pin NAME [--keep-focus] [--notify]"

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
		name, p, err := named()
		if err != nil {
			return "", err
		}
		wasOn := w.OnWall()[name]
		ok, err := w.Add(p)
		switch {
		case err != nil:
			return "", err
		case !ok:
			return wall.AddFullMsg, nil
		case wasOn:
			return name + " on wall", nil
		}
		return name + " added to wall", nil
	case "hide":
		name, p, err := named()
		if err != nil {
			return "", err
		}
		return name + " off wall", w.Hide(p)
	case "show":
		name, p, err := named()
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
			return wall.FullMsg, nil
		}
		return wall.ShownMsg(name, before, wasOn, before != "" && w.OnWall()[before]), nil
	}
	return "", fmt.Errorf("unknown wall command %q", args[0])
}
