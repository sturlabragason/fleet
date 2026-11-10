// Package fleet implements the core operations behind the CLI and TUI:
// spawn, refresh (status), send, peek and kill. It is the only place that
// combines tmux, tools, status and board.
package fleet

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/playbook"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/summary"
	"github.com/sturlabragason/fleet/internal/team"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/tools"
	"github.com/sturlabragason/fleet/internal/worktree"
)

const (
	// Socket is the dedicated tmux server for agents.
	Socket = "fleet"
	// Session holds every agent window and the main window.
	Session = "fleet"
	// WindowPrefix names hidden per-agent windows.
	WindowPrefix = "ag-"
)

// Fleet bundles the dependencies.
type Fleet struct {
	T        *tmux.Client
	Store    *board.Store
	Home     string
	Exe      string
	Profiles map[string]tools.Profile
	Patterns status.Sets
	// Prefs are the local, uncommitted spawn preferences (ADR-13).
	Prefs *prefs.Prefs
	// Teams is teams.json in the same home (DESIGN.md §5c).
	Teams *team.Store
	// Paste types a mail nudge into a pane and presses Enter; nil = tmux
	// paste (tests replace it).
	Paste func(pane, text string) error
	// SupPane reads the admiral pane: idle or not and its input box; nil =
	// tmux and the admiral profile's patterns (tests replace it).
	SupPane func() AdmiralView
	// AdmiralPane is SupPane for an admiral other than main (§6.12).
	AdmiralPane func(name string) AdmiralView
	// TypeText pastes text into a pane without Enter and PressKeys sends tmux
	// key names; nil = tmux (tests replace them). Report wakes use them so
	// Enter is pressed only after the input box is checked (ADR-36).
	TypeText  func(pane, text string) error
	PressKeys func(pane string, keys ...string) error
	// CloseLater schedules closing an agent after its report returns; nil =
	// tmux run-shell (tests replace it).
	CloseLater func(name string) error
	// Clients lists the attached tmux clients (active pane, last key
	// press); nil = tmux list-clients (tests replace it, ADR-61).
	Clients func() []ClientSeen
	// Go runs a job off the caller's goroutine: the done-check after a done
	// report (ADR-43), which may take minutes. The MCP servers set it so the
	// agent's fleet_report returns at once; nil runs the job inline, which
	// is what the one-shot CLI (the process exits on return) and tests need.
	Go func(func())
	// OffWallEvery is how often Refresh captures an agent pane that is not
	// on the wall; 0 = every Refresh. The overview TUI sets it (DESIGN §7a).
	OffWallEvery time.Duration
	// ShareSnapshot, when set, makes Refresh read tmux with one snapshot
	// (every pane plus the window options the wall reads) instead of a plain
	// list-panes, and hands it over with this refresh's own pane tags
	// applied. The watcher sets it and writes the snapshot to a file the
	// overview reads instead of running tmux (ADR-98).
	ShareSnapshot func(*tmux.Snapshot)
	// DrainQueue lets Refresh start the oldest queued agent when a working
	// slot is free, and lets the memory guard park an idle agent when RAM
	// runs low (DESIGN §7, §7a). The overview TUI, the admiral's MCP
	// server and `fleet ls` set it; the board lock keeps two of them from
	// starting the same agent twice.
	DrainQueue bool
	// DrainSync launches a claimed queued agent in this goroutine instead
	// of a background one. One-shot commands (`fleet ls`) set it: their
	// process exits before a goroutine could finish the launch.
	DrainSync bool
	// Housekeeping runs the once-a-poll jobs that belong to a resident
	// fleet: admiral wakes, the done sweep and dead-pane reaping. The
	// watcher sets it (it is the resident poll, ADR-49); the overview TUI
	// sets it too and does the work itself only while no watcher is live.
	Housekeeping bool
	// FreeMem reads free memory for the guard; nil = /proc/meminfo (tests
	// replace it).
	FreeMem func() (uint64, error)
	// SummaryModel replaces the model behind PeekSummary; nil builds the
	// CLI backend from the [summary] section of config.toml. Tests set it.
	SummaryModel summary.Model
	// LookCmd reports whether a profile's command can be run; nil = look
	// in ~/.local/bin, PATH and a login shell (tests replace it).
	LookCmd     func(cmd string) error
	summarizer  *summary.Summarizer
	summaryMu   sync.Mutex
	wakeMu      sync.Mutex
	wakeWarn    time.Time
	lastCap     map[string]time.Time
	lastRecycle time.Time         // last recycle check (ADR-71)
	globs       map[string]string // agent -> resolved session log (WatchPaths)
	globMu      sync.Mutex
	sweep       doneSweep
	dead        deadReaper
}

// Open builds a Fleet on the default socket and ~/.fleet.
func Open() (*Fleet, error) {
	home := logx.Home()
	exe, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return New(tmux.New(Socket), home, exe)
}

// New builds a Fleet from parts (used by tests).
func New(t *tmux.Client, home, exe string) (*Fleet, error) {
	pr, err := prefs.Load()
	if err != nil {
		// A broken local file must not make fleet unusable; log and carry on.
		logx.For("fleet").Error("config.toml", "err", err)
		pr = nil
	}
	ps, err := tools.LoadWith(home, pr.ToolsDoc())
	if err != nil {
		return nil, err
	}
	return &Fleet{T: t, Store: board.NewStore(home), Home: home, Exe: exe, Profiles: ps, Patterns: status.Load(home), Prefs: pr, Teams: team.NewStore(home)}, nil
}

// Async makes slow work (today: a done-check, ADR-43) run on its own
// goroutine. A process with a loop that must not stall — the MCP servers and
// the overview — calls it; a one-shot command leaves it alone so the work
// finishes before the process exits, which is also what keeps tests
// deterministic.
func (f *Fleet) Async() { f.Go = func(fn func()) { go fn() } }

// errNotStarting: a queued agent was closed or changed while it was launched.
var errNotStarting = errors.New("no longer starting (closed while launching)")

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)

// validName reports whether name can be an agent name. "user", "admiral" and
// its old name "supervisor" are reserved mailbox names (DESIGN §6.6, §6.7).
func validName(name string) bool {
	return nameRe.MatchString(name) && name != mail.User && name != mail.Admiral && name != mail.LegacySupervisor &&
		!strings.HasPrefix(name, mail.AdmiralPrefix)
}

// SpawnOpts are the per-spawn overrides of the config file. A nil field
// leaves the profile's `config.toml` setting alone.
type SpawnOpts struct {
	// Worktree overrides [defaults]/[profile.<name>] worktree: the agent
	// gets its own git worktree, or works straight in dir (DESIGN §4).
	Worktree *bool
	// Card is the task card: goal, done-check and result file (ADR-43).
	// nil = no card, and the spawn behaves as it did before cards.
	Card *board.Card
	// Role is the agent's place in the hierarchy (ADR-53): a leader of its
	// own squad, a member of one, or neither (the zero value).
	Role Role
	// Effort is the worker playbook's level: light, standard or pro
	// (ADR-57). "" picks the default for the spawn: pro with a worktree,
	// light without one.
	Effort string
	// Driver is "tmux" or "acp" (DESIGN §7d, ADR-74); "" takes the
	// profile's driver from config.toml, else tmux.
	Driver string
}

// Spawn starts an agent in its own hidden window, with the config file's
// spawn settings.
func (f *Fleet) Spawn(name, profile, dir, prompt string) (*board.Agent, error) {
	return f.SpawnWith(SpawnOpts{}, name, profile, dir, prompt)
}

// SpawnWith is Spawn with per-spawn overrides (the CLI flags and the
// fleet_spawn MCP arguments).
func (f *Fleet) SpawnWith(o SpawnOpts, name, profile, dir, prompt string) (*board.Agent, error) {
	if !validName(name) {
		return nil, fmt.Errorf("bad name %q", name)
	}
	if _, ok := f.Profiles[profile]; !ok {
		return nil, fmt.Errorf("unknown profile %q (have %v)", profile, tools.Names(f.Profiles))
	}
	if old, exists := f.Store.Load().Agents[name]; exists {
		if old.Status == string(status.Parked) {
			return nil, fmt.Errorf("agent %q is parked; resume it with fleet wake %s, or close it first", name, name)
		}
		if pending(old.Status) || f.paneOf(name) != "" {
			return nil, fmt.Errorf("agent %q already exists", name)
		}
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if err := checkDir(dir); err != nil {
		return nil, err
	}
	// Never start an agent in the home directory (ADR-47); do it before the
	// queue so a queued agent remembers where it will really start.
	dir, _ = f.safeDir(name, dir)
	wt := f.Prefs.For(profile).Worktree
	if o.Worktree != nil {
		wt = *o.Worktree
	}
	if o.Effort != "" {
		l, err := playbook.ParseLevel(o.Effort)
		if err != nil {
			return nil, err
		}
		o.Effort = string(l) // the board keeps the canonical name, not "PRO"
	}
	driver, err := f.driverFor(o.Driver, profile)
	if err != nil {
		return nil, err
	}
	if err := f.CheckCommand(profile, f.Profiles[profile], driver); err != nil {
		return nil, err
	}
	if driver == prefs.DriverACP {
		if err := acpSocketFits(f.Home, name); err != nil {
			return nil, err
		}
	}
	card := NormalizeCard(o.Card)
	if err := f.checkRole(name, o.Role); err != nil {
		return nil, err
	}
	if o.Role.Leader {
		f.MakeLeader(name)
	}
	if a, err := f.enqueue(name, profile, dir, prompt, wt, card, o.Role, o.Effort, driver); a != nil || err != nil {
		return a, err
	}
	// The busy count and the launch are not one critical section, so two
	// concurrent spawns can both see the last free slot: the cap is
	// best-effort and may be exceeded by one per racing spawn.
	return f.launch(name, profile, dir, prompt, false, wt, card, o.Role, o.Effort, driver)
}

// agentWorktree gives the agent its own worktree and branch for a spawn into
// a git repo (DESIGN §4, ADR-39). A dir outside git simply has none. When git
// refuses (no commit yet, the branch is checked out elsewhere), the spawn
// falls back to dir with a logged warning rather than failing: a usable agent
// in a shared checkout beats no agent at all. inRepo says the dir was a git
// repo whatever came of the worktree, which is what makes it a code task for
// the playbook level (ADR-57).
func (f *Fleet) agentWorktree(name, dir string) (w *board.Worktree, inRepo bool) {
	log := logx.For("fleet")
	got, err := worktree.Create(dir, name)
	switch {
	case errors.Is(err, worktree.ErrNoRepo):
		log.Debug("no worktree: not a git repo", "agent", name, "dir", dir)
		return nil, false
	case err != nil:
		log.Warn("worktree failed; spawning in the dir itself", "agent", name, "dir", dir, "err", err)
		return nil, true
	}
	log.Info("worktree", "agent", name, "path", got.Path, "branch", got.Branch)
	return &board.Worktree{Path: got.Path, Branch: got.Branch, Repo: got.Repo}, true
}

// effortLevel resolves a spawn's effort argument to a playbook level: what
// the spawn asked for, else the default for whether this is a code task with
// a worktree of its own (ADR-57). An unparseable value cannot reach here from
// SpawnWith, so a stale board entry simply falls back to the default.
func effortLevel(effort string, code bool) playbook.Level {
	if effort != "" {
		if l, err := playbook.ParseLevel(effort); err == nil {
			return l
		}
		logx.For("fleet").Warn("unknown effort; using the default", "effort", effort)
	}
	return playbook.DefaultLevel(code)
}

// contextKey picks the profile's ContextArgs entry for an agent (ADR-60): a
// leader's own entry when the profile has one, else its effort level.
func contextKey(p tools.Profile, lvl playbook.Level, leader bool) string {
	if _, ok := p.ContextArgs["leader"]; ok && leader {
		return "leader"
	}
	return string(lvl)
}

// launch builds the argv for a profile and starts it: the real start behind
// Spawn and the queue drain. The first prompt is built here only. claimed is
// true for a queued agent the drain marked starting (see start). wantWT asks
// for the agent's own git worktree; the playbook and the baseline are then
// read from that worktree, not from the spawn dir. card is the task card
// (ADR-43): it goes on the board and its block is appended to the prompt.
func (f *Fleet) launch(name, profile, dir, prompt string, claimed, wantWT bool, card *board.Card, role Role, effort, driver string) (*board.Agent, error) {
	p, ok := f.Profiles[profile]
	if !ok {
		return nil, fmt.Errorf("unknown profile %q (have %v)", profile, tools.Names(f.Profiles))
	}
	ex := startExtra{NoWorktree: !wantWT, Role: role, Effort: effort}
	// code is "the agent works on a git repo of its own": a worktree was
	// asked for and the dir is one. It is what picks the playbook level when
	// the spawn did not (ADR-57), so a worktree git refused still gets the
	// full process — that is the shared checkout where it matters most.
	code := false
	if wantWT {
		var inRepo bool
		ex.Worktree, inRepo = f.agentWorktree(name, dir)
		code = inRepo
		if ex.Worktree != nil {
			dir = ex.Worktree.Path
		}
	}
	// Only now is it known whether the agent shares its dir, which decides
	// its default result file.
	card = ResultFor(card, name, ex.Worktree != nil)
	ex.Card = card
	// The board records the level actually used, so wake can give the
	// resumed process the same context args (ADR-60).
	lvl := effortLevel(effort, code)
	ex.Effort = string(lvl)
	acp := driver == prefs.DriverACP
	// The dir is final here (worktree or not), so this is the last moment
	// before the tool reads it: pre-answer its trust dialog (ADR-47). An
	// ACP server shows no dialog.
	marked := acp || f.TrustDir(profile, p, dir)
	id := ""
	if len(p.LaunchID) > 0 {
		id = tools.NewUUID()
	}
	pr := f.Prefs.For(profile)
	pr.Mode = f.modeFor(name, profile, p, pr.Mode)
	// The worker playbook goes in front of the first prompt only; the board
	// keeps the user's prompt as the goal (DESIGN §4, ADR-22). No prompt, no
	// playbook. A leader also gets its squad orientation, at every effort
	// level and whether or not the playbook is on (ADR-58).
	full := prompt
	lead := ""
	if role.Leader {
		lead = playbook.Leader()
	}
	// An interactive agent is told it reports progress, not done, until
	// the user ends the session (ADR-61), playbook on or off.
	if role.Interactive {
		lead = strings.TrimSpace(lead + "\n\n" + playbook.Interactive())
	}
	switch {
	case pr.Playbook && prompt != "":
		text, src := playbook.Resolve(lvl, f.Home, dir)
		if lead != "" {
			text = strings.TrimRight(text, "\n") + "\n\n" + lead
		}
		full = playbook.Compose(text, prompt)
		logx.For("fleet").Info("playbook", "agent", name, "effort", lvl, "sources", src, "bytes", len(full)-len(prompt))
	case lead != "" && prompt != "":
		full = playbook.Compose(lead, prompt)
	}
	// The task card goes last, so it is the final thing the agent reads
	// (ADR-43). A card with no prompt is still worth telling the agent.
	if cp := CardPrompt(card); cp != "" {
		if full != "" {
			full += "\n\n"
		}
		full += cp
	}
	if acp {
		ex.Base = Baseline(dir)
		return f.launchACP(name, profile, p, dir, prompt, full, claimed, lvl, ex)
	}
	argv, embedded := launchArgv(name, p, f.launchVars(name, id, p, pr, fenceDir(ex.Worktree, pr.FenceWrites), false),
		tools.Launch{Mode: pr.Mode, Context: contextKey(p, lvl, role.Leader), Extra: pr.ExtraArgs,
			Prompt: full, NoPromptArgs: !pr.AutoSubmit})
	ex.Base = Baseline(dir) // before the agent can touch anything
	a, err := f.start(name, profile, p, dir, prompt, full, id, argv, claimed, ex)
	if err != nil {
		return nil, err
	}
	// Before autostart: the dialog reads as waiting, so an unanswered one
	// would swallow the first prompt (ADR-47).
	if !marked {
		f.answerTrust(f.T, time.Sleep, a.PaneID, p, pr)
	}
	f.autostart(f.T, time.Sleep, a.PaneID, p, pr, full, embedded)
	return a, nil
}

// modeFor returns the starting mode to launch an agent with: the configured
// one when the profile knows it, "" otherwise. An unknown mode is logged and
// ignored rather than failing the launch, so a typo costs the mode, not the
// agent. Resume paths use it too, so a woken or adopted agent comes back in
// the same mode it started in (ADR-56).
func (f *Fleet) modeFor(name, profile string, p tools.Profile, mode string) string {
	if p.HasMode(mode) {
		return mode
	}
	logx.For("fleet").Warn("unknown mode for profile; ignoring",
		"agent", name, "profile", profile, "mode", mode)
	return ""
}

// MaxLaunchCmd caps the shell command a spawn hands to tmux new-window with
// the prompt embedded. tmux rejects commands over about 16KB ("command too
// long"), and the playbook makes first prompts long.
const MaxLaunchCmd = 12000

// launchArgv builds the launch argv; when an embedded prompt would make the
// command too long for tmux, it rebuilds without it so the prompt goes via
// the paste path instead.
// fenceDir is the dir a Claude agent's file tools may write in besides its
// own: the <repo>.wt/ dir its fleet worktree sits in, or "" for no fence
// (no worktree, or fence_writes = false) (ADR-79).
func fenceDir(w *board.Worktree, on bool) string {
	if w == nil || w.Path == "" || !on {
		return ""
	}
	return filepath.Dir(w.Path)
}

// launchVars prepares launching worker name with profile p and returns its
// Vars: the peer MCP server and hooks, with the user's global MCP servers
// switched off except those pr keeps (ADR-91), and the write fence for
// fence (ADR-79). When p loads the fleet plugin it writes the agent's
// plugin dir and points the Vars at it (ADR-99); if that fails the agent
// starts without it, on the hook settings and peer args as before. The
// subagents an earlier process of this name left are dropped: they died
// with it.
func (f *Fleet) launchVars(name, id string, p tools.Profile, pr prefs.Spawn, fence string, acp bool) tools.Vars {
	v := tools.AgentVars(f.Exe, name, id).UserMCP(p, pr.KeepMCP)
	status.DropHookSubs(f.Home, name)
	if p.UsesPlugin(acp) {
		dir := tools.PluginDir(f.Home, name)
		pl := tools.Plugin{Exe: f.Exe, Name: name, MCP: p.PluginMCP, NoStatus: acp}
		if err := tools.WritePlugin(dir, pl); err != nil {
			logx.For("fleet").Warn("plugin dir; starting without it", "agent", name, "dir", dir, "err", err)
		} else {
			v = v.WithPlugin(dir, p)
		}
	}
	if acp {
		// The host writes the status; the settings only fence (ADR-99).
		v.Hooks = tools.ClaudeFence(fence)
		return v
	}
	return v.Fenced(fence)
}

func launchArgv(name string, p tools.Profile, v tools.Vars, l tools.Launch) ([]string, bool) {
	argv, embedded := p.Argv(v, l)
	if n := len(tools.ShellCommand(argv)); embedded && n > MaxLaunchCmd {
		logx.For("fleet").Warn("prompt too long for the launch command; pasting it instead",
			"agent", name, "profile", p.Name, "bytes", n, "max", MaxLaunchCmd)
		l.NoPromptArgs = true
		argv, embedded = p.Argv(v, l)
	}
	return argv, embedded
}

// redactArgv replaces an embedded first prompt with a placeholder so the whole
// prompt does not end up in ~/.fleet/logs/fleet.log on every spawn.
func redactArgv(argv []string, prompt string) []string {
	if prompt == "" {
		return argv
	}
	out := make([]string, len(argv))
	for i, a := range argv {
		if strings.Contains(a, prompt) {
			out[i] = fmt.Sprintf("<prompt:%d bytes>", len(prompt))
			continue
		}
		out[i] = a
	}
	return out
}

// start runs argv in a new hidden agent window, tags the pane and writes the
// board entry. Shared by Spawn, Adopt and the queue drain. goal is the board
// goal; secret is the text redacted from the logged argv (the full first
// prompt). claimed: the board entry must still be "starting" once the pane
// exists, else the agent was closed mid-launch and the new pane is killed.
// ex carries the board fields worked out before the pane exists.
func (f *Fleet) start(name, profile string, p tools.Profile, dir, goal, secret, id string, argv []string, claimed bool, ex startExtra) (*board.Agent, error) {
	log := logx.For("fleet")
	if extra := f.Prefs.For(profile).Env; len(extra) > 0 {
		env := make(map[string]string, len(p.Env)+len(extra))
		for k, v := range p.Env {
			env[k] = v
		}
		for k, v := range extra {
			env[k] = v
		}
		p.Env = env
	}
	pane, err := f.T.Spawn(Session, WindowPrefix+name, dir, p.EnvList(), tools.ShellCommand(argv))
	if err != nil {
		log.Error("spawn failed", "agent", name, "err", err)
		return nil, err
	}
	f.T.SetPaneOption(pane, "@fleet_agent", name)
	f.T.SetPaneOption(pane, "@role", "agent")
	f.T.SetPaneOption(pane, "remain-on-exit", "on")
	f.T.Run("set-option", "-s", "-t", Session, "escape-time", "10")
	f.T.Run("set-option", "-g", "mouse", "on")
	if afterTag != nil {
		afterTag(f, pane)
	}
	now := time.Now()
	a := &board.Agent{Name: name, Profile: profile, Patterns: p.Patterns, Dir: dir, Goal: goal, SessionID: id,
		PaneID: pane, Status: string(status.Unknown), Created: now, StatusSince: now, LastChange: now,
		Baseline: ex.Base, Worktree: ex.Worktree, NoWorktree: ex.NoWorktree, Card: ex.Card, Effort: ex.Effort,
		Leader: ex.Role.Leader, LeadBy: ex.Role.LeadBy, Interactive: ex.Role.Interactive, Driver: ex.Driver,
		FleetVersion: Version}
	if a.ACP() {
		a.ACPServer = p.ACPLabel()
	}
	if goal != "" && secret != "" {
		a.PromptAt = now // a spawn with a task: the turn it starts should end in a report (ADR-68)
	}
	a.SetOwner(ex.Role.Admiral)
	if err := f.Store.Update(func(b *board.Board) error {
		old, ok := b.Agents[name]
		if claimed {
			// A Refresh between the pane tag and this write may already have
			// adopted this pane and reclassified it; that is still our claim.
			if !ok || (old.Status != string(status.Starting) && old.PaneID != pane) {
				return errNotStarting
			}
			a.Created = old.Created // a queued agent keeps its place in time
		} else if ok && ex.Resume && old.PaneID != pane {
			// Resume checked the name before its ready wait, not under the lock.
			return fmt.Errorf("agent %q already exists", name)
		}
		b.Agents[name] = a
		delete(b.Closed, name) // a new agent of the name is not the closed one
		return nil
	}); err != nil {
		f.KillPane(pane)
		log.Error("board write failed", "agent", name, "err", err)
		return nil, err
	}
	log.Info("started", "agent", name, "profile", profile, "dir", dir, "pane", pane, "session_id", id, "argv", redactArgv(argv, secret))
	return a, nil
}

// startExtra are the board fields spawn and adopt work out before the pane
// exists.
type startExtra struct {
	Base       *board.GitBase
	Worktree   *board.Worktree
	NoWorktree bool
	Card       *board.Card
	Role       Role
	Effort     string
	Driver     string // board.Agent.Driver
	Resume     bool   // a resume: the name must still be free (ADR-95)
}

// Version is the running fleet build ("v1.2.0", else a revision), set by
// main; start records it on the board (ADR-77). "" in tests.
var Version string

// afterTag, when set by a test, runs between tagging a new pane and the
// board write in start.
var afterTag func(f *Fleet, pane string)

// paneOf finds the live pane for an agent by its @fleet_agent tag.
func (f *Fleet) paneOf(name string) string {
	ps, _ := f.T.ListPanes("")
	for _, p := range ps {
		if p.Agent == name {
			return p.ID
		}
	}
	return ""
}

// Pane returns the pane id of an agent or an error.
func (f *Fleet) Pane(name string) (string, error) {
	if p := f.paneOf(name); p != "" {
		return p, nil
	}
	if a, ok := f.Store.Load().Agents[name]; ok {
		if pending(a.Status) {
			return "", fmt.Errorf("agent %q is %s, not started yet", name, a.Status)
		}
		if a.Status == string(status.Parked) {
			return "", fmt.Errorf("agent %q is parked; fleet wake %s resumes it", name, name)
		}
	}
	return "", fmt.Errorf("agent %q not running", name)
}

// SubmitPause is the wait between pasting and pressing Enter, and
// SubmitWait how long Send watches for a paste the tool did not submit.
var (
	SubmitPause = 150 * time.Millisecond
	SubmitWait  = 2 * time.Second
)

// Send pastes text into the agent and presses Enter. When the agent's
// patterns have `pending` and the paste still sits unsubmitted after
// SubmitWait, it presses Enter once more, then gives up with an error.
func (f *Fleet) Send(name, text string) error {
	if f.isACP(name) {
		return f.acpSend(name, text)
	}
	p, err := f.Pane(name)
	if err != nil {
		return err
	}
	logx.For("fleet").Info("send", "agent", name, "bytes", len(text))
	if err := f.T.Paste(p, text, false); err != nil {
		return err
	}
	time.Sleep(SubmitPause)
	if err := f.T.SendKeys(p, "Enter"); err != nil {
		return err
	}
	f.notePrompt(name)
	set := f.setOf(name)
	if len(set.Pending) == 0 || !f.unsubmitted(p, set, SubmitWait) {
		return nil
	}
	// Enter on a prompt that came up meanwhile would answer it (ADR-9).
	if w, _ := f.Prompting(name); w != "" {
		return fmt.Errorf("pasted but not submitted; try fleet_send keys=[Enter]")
	}
	logx.For("fleet").Warn("paste not submitted; pressing Enter again", "agent", name)
	if err := f.T.SendKeys(p, "Enter"); err != nil {
		return err
	}
	time.Sleep(SubmitPause)
	if f.unsubmitted(p, set, 0) {
		return fmt.Errorf("pasted but not submitted; try fleet_send keys=[Enter]")
	}
	return nil
}

// notePrompt records that the agent was just handed work, so a turn that
// ends without a report after it shows as "no report" (ADR-68).
func (f *Fleet) notePrompt(name string) {
	now := time.Now()
	err := f.Store.Update(func(b *board.Board) error {
		if a, ok := b.Agents[name]; ok {
			a.PromptAt = now
		}
		return nil
	})
	if err != nil {
		slog.Warn("note prompt", "component", "fleet", "agent", name, "err", err)
	}
}

// unsubmitted polls the pane for up to wait and reports whether the pending
// pattern is still on screen at the end.
func (f *Fleet) unsubmitted(pane string, set status.Set, wait time.Duration) bool {
	end := time.Now().Add(wait)
	for {
		text, err := f.T.Capture(pane, 0)
		if err != nil {
			return false
		}
		if _, ok := set.Unsubmitted(text); !ok {
			return false
		}
		if time.Now().After(end) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// setOf is the status pattern set of an agent on the board.
func (f *Fleet) setOf(name string) status.Set {
	if a, ok := f.Store.Load().Agents[name]; ok {
		return f.Patterns[a.Patterns]
	}
	return status.Set{}
}

// Peek returns raw pane text (last n lines).
func (f *Fleet) Peek(name string, n int) (string, error) {
	p, err := f.Pane(name)
	if err != nil {
		return "", err
	}
	if f.isACP(name) {
		return f.acpScreen(name, n), nil
	}
	return f.T.Capture(p, n)
}

// PeekColor returns the pane with its SGR colour escapes (last n lines of
// scrollback plus the screen), for front ends that draw colour (fleet web).
func (f *Fleet) PeekColor(name string, n int) (string, error) {
	p, err := f.Pane(name)
	if err != nil {
		return "", err
	}
	if f.isACP(name) {
		return f.acpScreen(name, n), nil
	}
	return f.T.Run("capture-pane", "-e", "-p", "-t", p, "-S", fmt.Sprint(-n))
}

// Close stops the agent and drops it from the board. Closing a leader
// leaves its members running and hands them back to the admiral (ADR-53).
func (f *Fleet) Close(name string) error {
	if p := f.paneOf(name); p != "" {
		acp := f.isACP(name)
		if acp {
			// Its host exits cleanly on the stop: not a death to report.
			f.markClosing(name, time.Now())
			f.acpStop(name)
		}
		if err := f.KillPane(p); err != nil {
			if acp {
				f.markClosing(name, time.Time{})
			}
			return err
		}
	}
	// After the kill, so a close that failed leaves the squad whole, and a
	// report that reached the leader while it died is still re-filed.
	f.disband(name)
	logx.For("fleet").Info("closed", "agent", name)
	f.forgetMail(name)
	f.forgetHook(name)
	if err := os.RemoveAll(tools.PluginDir(f.Home, name)); err != nil {
		logx.For("fleet").Warn("remove plugin dir", "agent", name, "err", err)
	}
	// Prompts a failed acp host kept are listed in its transcript and the
	// dead-pane report; a closed agent has no next host to send them.
	if err := os.Remove(ACPPromptPath(f.Home, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		logx.For("fleet").Warn("acp: remove prompt file", "agent", name, "err", err)
	}
	team := ""
	if f.Teams != nil {
		team, _ = f.Teams.Of(name)
		if err := f.Teams.DropAgent(name); err != nil {
			logx.For("fleet").Warn("drop team membership", "agent", name, "err", err)
		}
	}
	var wt *board.Worktree
	err := f.Store.Update(func(b *board.Board) error {
		a, ok := b.Agents[name]
		if !ok {
			return fmt.Errorf("no agent %q", name)
		}
		wt = a.Worktree
		// Kept so a send can resume it with its card (ADR-95); without a
		// session there is nothing to resume.
		if a.SessionID != "" {
			b.AddClosed(a, team, time.Now())
		}
		delete(b.Agents, name)
		return nil
	})
	if err == nil {
		dropMergedRemote(name, wt)
	}
	return err
}

// dropMergedRemote deletes a closed agent's pushed fleet/ branch from the
// remote once it is merged into main, so CI branches don't pile up on the
// remote (ADR-87). The local branch and worktree stay (§4).
func dropMergedRemote(name string, wt *board.Worktree) {
	if wt == nil || wt.Branch == "" {
		return
	}
	dir := wt.Repo
	if dir == "" {
		dir = wt.Path
	}
	if dir == "" {
		return
	}
	ok, err := worktree.DeleteMergedRemote(dir, wt.Branch)
	switch {
	case err != nil:
		logx.For("fleet").Warn("remote branch not deleted", "agent", name, "branch", wt.Branch, "err", err)
	case ok:
		logx.For("fleet").Info("deleted merged remote branch", "agent", name, "branch", wt.Branch)
	}
}
