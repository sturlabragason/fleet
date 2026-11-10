package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fake"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/sessions"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/team"
	"github.com/sturlabragason/fleet/internal/theme"
	"github.com/sturlabragason/fleet/internal/tools"
)

// loadTheme applies [defaults] theme from config.toml, once, before anything
// renders (ADR-55). An unknown name stops fleet; a config file that does not
// parse is left to the command that needs it to report.
func loadTheme() error {
	p, err := prefs.Load()
	if err != nil {
		return nil
	}
	return theme.Load(p.Theme())
}

const usage = `usage:
  fleet up                                  start/attach the fleet (admiral + overview + wall)
  fleet [--admiral-profile NAME] up [--no-attach]
                                            run the admiral under profile NAME (alone: fleet up); default
                                            $FLEET_ADMIRAL_PROFILE, last choice, [admiral] profile, copilot
  fleet ui                                  run the overview TUI in this terminal
  fleet spawn [--worktree|--no-worktree] [--leader] [--interactive] [--effort LEVEL] [--admiral NAME] <name> <profile> [dir] ["prompt"]
             [--driver tmux|acp] [--goal "one line"] [--check "cmd"] [--check-exit N] [--result FILE]
                                            in a git repo the agent gets its own worktree,
                                            <repo>.wt/<name> on branch fleet/<name> (worktree in
                                            config.toml turns it off; --no-worktree for one spawn);
                                            --goal/--check/--result give it a task card: fleet runs
                                            the check in its dir when it reports done and tells the
                                            admiral pass or fail, and the agent is told to write the
                                            result file (default result.md, or result-<name>.md
                                            when the agent has no worktree of its own);
                                            --effort light|standard|pro sizes the worker playbook
                                            (default: pro with a worktree, light without one);
                                            --driver acp runs the tool's ACP server under fleet acp-host
                                            instead of its screen (copilot, claude-copilot, agy, fake),
                                            --driver tmux the tool's own screen; default: driver in
                                            config.toml, else acp for profiles running copilot or agy
                                            (claude-copilot too), tmux otherwise;
                                            --leader makes it a leader: it gets squad tools, spawns a
                                            squad of its own and its agents report to it, not to the
                                            admiral ([squad] max_members, max_depth in config.toml);
                                            --interactive: the user talks to it directly, so it is
                                            never auto-closed and reports progress, not done;
                                            queued instead when max_working agents are busy;
                                            the overview TUI starts it when a slot frees
  fleet spawn --fake [--driver acp] <name> [dir]
                                            shorthand for profile "fake"
  fleet adopt [--name N] [--dir D] [--force] [--driver tmux|acp] [profile] <id | words...>
                                            resume an existing session (started outside fleet) as an agent;
                                            by ID, unique ID prefix (8+ chars) or words from fleet sessions;
                                            --driver as for spawn
  fleet sessions [--all] [--json] [-n 20] [words...]
                                            list copilot/claude sessions to adopt, newest first
  fleet ls [--json]                         the agents; DRIVER is acp or tmux
  fleet info <name>                         one agent's facts, as fleet_info: driver (and ACP server),
                                            the fleet version that started it, status, git, session, spend
  fleet spend [--json]                      what each agent has cost so far and the fleet total, read from
                                            the agents' own session files (AI credits for copilot, tokens
                                            for claude); no model is called
  fleet send [--force] <name> "text"        refused while the user is typing in that pane (keys
                                            there in the last 30 s, unsubmitted text in its box);
                                            --force types it anyway
  fleet peek <name> [--raw] [-n 200]
  fleet peek --summary <name>               about 5 lines from a cheap model: what it is doing, what it
                                            waits for, any error (cached per screen; [summary] in config.toml)
  fleet peek --live <name>                  live colour view of the agent's pane, 4x a second (Space in
                                            the overview opens it in a popup; Enter puts it in ▶)
  fleet close <name> [--squad]              stop an agent and remove it from the board
                                            (--squad: a leader's agents go with it; without it they
                                            keep running and report to the admiral)
  fleet park <name>                         stop an idle agent's process to free RAM, keep it on the board
                                            as "parked" with its session id (refused while it works,
                                            waits, or sits on a prompt)
  fleet wake <name> [--force]               resume a parked agent: the same session, dir and name
                                            (--force: wake even while memory is still low)
  fleet team ls [--json]                    teams with colour and members (running members marked)
  fleet team create <team> [colour]         new empty team; colour: blue, pink, teal, mauve, sky, sapphire or #rrggbb
  fleet team delete <team>                  remove the team; its agents keep running
  fleet team add <team> <agent>...          add agents (moves them from another team; creates the team)
  fleet team rm <team> <agent>...           take agents out of the team
  fleet team color <team> <colour>          change the team colour
  fleet team send <team> "text"             paste into every running member
  fleet team show|hide <team>               put running members on the wall / take them off
  fleet team close <team> [--yes]           close every member (asks y/N unless --yes)
  fleet mcp [--agent NAME]                  stdio MCP server: admiral tools, or the agent peer tools
                                            with --agent or when run inside an agent pane; re-execs itself
                                            in place when the fleet binary changes (--probe: self-check)
  fleet mail [--as NAME] agents | inbox [--all] [-n N] | send <to|*> "text"
             | request <to> ["reason"] | grant <who> | deny <who> | revoke <who>
                                            message other agents (free within a team or repo, else on request); --as defaults to this pane's agent
  fleet access [ls] | log [-n N] | grant A B | deny A B | revoke A B
                                            list, read or decide agent-to-agent access
  fleet report [--as NAME] <done|blocked|question|progress> "summary"
                                            report to the admiral (for agents without MCP)
  fleet reports [--all] [--limit N] [--as NAME] [--admiral NAME]
                                            read agent reports (what fleet_reports gives the admiral):
                                            unread ones, oldest first; --all shows the last N instead.
                                            Only the admiral (a shell in no agent pane, no --as) marks
                                            them read; an agent reads the history without taking them
  fleet wait [--agents a,b] [--timeout 300s] [--as NAME] [--admiral NAME]
                                            block until a new report or message arrives (an agent: its
                                            own mailbox; else the admiral's), max 10m; prints the
                                            headlines, marks nothing read. Exit 0 on news, 2 on timeout
  fleet admiral [--name N] [--json]         admiral session, context fill, generation, uptime
  fleet admiral add NAME [--profile P]      start another admiral beside main (DESIGN §6.12)
  fleet admiral ls                          admirals with their agents and unread reports
  fleet admiral close NAME                  close one; its agents and unread reports go back to main
  fleet admiral give AGENT [--to NAME]      hand an agent (and its unread reports) to another admiral (overview O)
  fleet admiral reset [--name N] [--handoff]         replace the admiral with a fresh session (M-R)
  fleet admiral profile [--name N] [NAME | --pick]   show the admiral's profile, or swap it (hands off; Alt-P picks)
  fleet admiral restart [--name N]          restart the admiral, resuming the same session
                                            ("fleet supervisor ..." still works: deprecated alias)
  fleet note [--agent NAME] [--admiral N] [--pin] "text"  append a note to ~/.fleet/decisions.log; --pin keeps it in every brief
  fleet playbook [--effort LEVEL] [dir]     print the worker playbook prepended to a spawn prompt in dir
                                            (built-in + ~/.fleet/worker.md + the repo's .fleet/worker.md;
                                            --effort light|standard|pro, default pro)
  fleet touching [-C dir] [--json] [file...]
                                            files other worktrees of this repo change; with files, only
                                            the overlaps (exit 0 none, 1 overlap, 2 error). Agents'
                                            uncommitted result files don't count
  fleet adr claim [--for AGENT] [-C dir]    take the next free ADR number for DESIGN.md §14 and print it;
                                            atomic, so parallel agents never pick the same one
  fleet adr ls [--json] [-C dir]            ADR numbers claimed but not yet on main
  fleet adr release <N> [-C dir]            give a number back (a claim also frees itself once its ADR is on main)
  fleet merge [-C dir] [--dry-run [--json]] [--check CMD] [--retries N]
                                            rebase this worktree's branch onto main, run the checks,
                                            merge into the local main checkout and run them again.
                                            Conflicts in CHANGELOG.md or DESIGN.md keep both sides,
                                            newest first; any other conflict aborts the rebase and
                                            exits 1, naming the files and the other worktree.
                                            --dry-run prints the order it would take. Never pushes,
                                            never removes the worktree
  fleet profiles                            list tool profiles (~/.fleet/tools.toml)
  fleet snap [-C dir] [--set NAME] [--socket S] <agent | pane-id> <state>
                                            save the agent's pane as a status fixture in
                                            internal/status/testdata/<tool>/<state>.txt, so the table test
                                            picks it up; state is working, waiting, idle, error or stuck
                                            (optionally <state>-<variant>). A tmux pane id needs --set, and
                                            --socket (fleet-test-*) to read a scratch tmux server
  fleet wall <apply|refresh|preset N|show NAME|emphasis>   wall control (used by tmux key bindings)
  fleet wall <add NAME|next|prev|untarget>  add a pane, next/previous waiting agent into the ▶ pane, clear ▶
  fleet wall <undo|pin NAME>                undo the last ▶ replace (again: redo); pin/unpin a pane
  fleet wall <hide-all|restore|toggle-all>  take every agent pane off the wall (agents keep running),
                                            put them back, or whichever fits (Alt-X)
                                            (--keep-focus: show/add leave keyboard focus where it is;
                                            --notify: the result goes to a tmux message, not stdout)
  fleet keys [--markdown]                   list every hotkey (Alt-? in tmux, ? in the overview)
  fleet changes [--json]                    what's new in fleet: its latest features, newest first (Alt-g)
  fleet version                             the release tag and commit this binary was built from
  fleet watch [--interval 1s] [--tick 200ms]
                                            run the status watcher in the foreground: it keeps the board,
                                            the pane border titles, fleet ls and the overview agreeing even
                                            with no window open. fleet up starts one in the background;
                                            it re-execs itself when bin/fleet is rebuilt
  fleet watch --once | --stop | --probe     refresh every status once / stop the running watcher / print ok
  fleet web [--listen ADDR] [--allow-host NAME,...]
                                            browser view of the fleet: live tiles, an agent's screen,
                                            answer its prompt, send a line or allowed keys (refused while
                                            you type in that pane). Listens on 127.0.0.1:7777
                                            ($FLEET_WEB_LISTEN); prints a URL with the token, kept in
                                            ~/.fleet/web.json ($FLEET_WEB_TOKEN overrides). A proxy sends
                                            "Authorization: Bearer <token>"; --allow-host ($FLEET_WEB_HOSTS)
                                            names the host the browser uses through it
  fleet hook <event> [--agent NAME] [--waiting-for TEXT]
                                            record what a tool's own hook just said (stop, notification,
                                            pretooluse, …) in ~/.fleet/status/<name>.json; fleet wires this
                                            into Claude per agent at spawn, never into your own settings

local preferences (never committed): ~/.config/fleet/config.toml ($FLEET_CONFIG overrides)
  sets the starting mode (mode = "auto": no permission prompts, any tool),
  extra args, prompt auto-submit,
  the worker playbook (playbook = false turns it off) and the per-agent git
  worktree (worktree = false turns it off);
  [defaults] max_working and the [memory] RAM guard (guard, min_free, check_every)
  are re-read live, so a change takes effect without restarting fleet;
  [defaults] theme picks the TUI palette: catppuccin (default), stealth,
  neon-drive, phosphor or graphite -- set it and run fleet up again;
  see examples/config.toml.example`

func run(args []string) error {
	args, err := topLevelProfile(args)
	if err != nil {
		return err
	}
	if err := loadTheme(); err != nil {
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("%s", usage)
	}
	cmd, rest := args[0], args[1:]
	if cmd == "fakeagent" {
		if code := fake.Main(rest, os.Stdin, os.Stdout, os.Stderr); code != 0 {
			os.Exit(code)
		}
		return nil
	}
	switch cmd { // need no fleet state or tmux
	case "keys":
		return cmdKeys(rest)
	case "playbook":
		return cmdPlaybook(rest)
	case "touching":
		return cmdTouching(rest)
	case "adr":
		return cmdADR(rest)
	case "merge":
		return cmdMerge(rest)
	case "acp-host":
		return cmdACPHost(rest)
	case "version", "--version":
		return cmdVersion(rest)
	}
	if cmd == "changes" {
		return cmdChanges(rest)
	}
	f, err := fleet.Open()
	if err != nil {
		return err
	}
	if err := leaderScope(f, commandName(cmd), rest); err != nil {
		return err
	}
	switch commandName(cmd) {
	case "spawn":
		return cmdSpawn(f, rest)
	case "adopt":
		return cmdAdopt(f, rest)
	case "sessions":
		return cmdSessions(f, rest)
	case "ls":
		return cmdLs(f, rest)
	case "spend":
		return cmdSpend(f, rest)
	case "info":
		if len(rest) != 1 {
			return fmt.Errorf("usage: fleet info <name>")
		}
		out, err := f.Info(rest[0])
		if err != nil {
			return err
		}
		fmt.Println(out)
		return nil
	case "send":
		force := len(rest) > 0 && rest[0] == "--force"
		if force {
			rest = rest[1:]
		}
		if len(rest) < 2 {
			return fmt.Errorf("usage: fleet send [--force] <name> \"text\"")
		}
		if r := f.SendBlocker(rest[0], true); r != "" && !force {
			return fmt.Errorf("not sent: %s; fleet send --force types it anyway", r)
		}
		return f.Send(rest[0], strings.Join(rest[1:], " "))
	case "peek":
		return cmdPeek(f, rest)
	case "close", "kill": // "kill" kept as a hidden alias
		squad := false
		if n := len(rest); n > 0 && rest[n-1] == "--squad" {
			squad, rest = true, rest[:n-1]
		}
		if len(rest) != 1 {
			return fmt.Errorf("usage: fleet close <name> [--squad]")
		}
		if squad {
			bad := 0
			for _, r := range f.SquadCloseAll(rest[0]) {
				if r.OK {
					n := ""
					if r.Note != "" {
						n = ". " + r.Note
					}
					fmt.Println("closed " + r.Agent + n)
					continue
				}
				bad++
				fmt.Fprintln(os.Stderr, r.Agent+": "+r.Note)
			}
			if bad > 0 {
				return fmt.Errorf("%d squad agent(s) not closed", bad)
			}
		} else if note := fleet.SquadNote(rest[0], f.Squad(rest[0])); note != "" {
			fmt.Println(note)
		}
		return f.Close(rest[0])
	case "park":
		if len(rest) != 1 {
			return fmt.Errorf("usage: fleet park <name>")
		}
		if err := f.Park(rest[0], ""); err != nil {
			return err
		}
		fmt.Printf("parked %s · fleet wake %s resumes it\n", rest[0], rest[0])
		return nil
	case "wake":
		force := false
		if n := len(rest); n > 0 && rest[n-1] == "--force" {
			force, rest = true, rest[:n-1]
		}
		if len(rest) != 1 {
			return fmt.Errorf("usage: fleet wake <name> [--force]")
		}
		ag, err := f.Wake(rest[0], force)
		if err != nil {
			return err
		}
		fmt.Printf("woke %s (%s) session %s in %s pane %s\n", ag.Name, ag.Profile, ag.SessionID, ag.Dir, ag.PaneID)
		return nil
	case "team":
		return cmdTeam(f, rest)
	case "mcp":
		return cmdMCP(f, rest)
	case "mail":
		return cmdMail(f, rest)
	case "access":
		return cmdAccess(f, rest)
	case "report":
		return cmdReport(f, rest)
	case "reports":
		return cmdReports(f, rest)
	case "wait":
		return cmdWait(f, rest)
	case "profiles":
		for _, n := range tools.Names(f.Profiles) {
			p := f.Profiles[n]
			argv, _ := p.Argv(tools.Vars{Fleet: "fleet", Name: "<name>", ID: "<id>", PeerMCP: "<peer-mcp>", PeerMCPClaude: "<peer-mcp>"}, tools.Launch{})
			fmt.Printf("%-16s %s  (patterns=%s)\n", n, strings.Join(argv, " "), p.Patterns)
		}
		fmt.Printf("\nprofiles: built-in + %s + [profile.*] in %s\n", tools.Path(f.Home), prefs.Path())
		return nil
	case "admiral": // also "supervisor", the deprecated alias (commandName, ADR-26)
		return cmdAdmiral(f, cmd, rest)
	case "note":
		return cmdNote(f, rest)
	case "ui":
		return cmdUI(f)
	case "up":
		return cmdUp(f, rest)
	case "wall":
		return cmdWall(f, rest)
	case "snap":
		return cmdSnap(f, rest)
	case "watch":
		return cmdWatch(f, rest)
	case "hook":
		return cmdHook(f, rest)
	case "web":
		return cmdWeb(f, rest)
	case "help", "-h", "--help":
		fmt.Println(usage)
		return nil
	}
	return fmt.Errorf("unknown command %q\n%s", cmd, usage)
}

func cmdSpawn(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("spawn", flag.ContinueOnError)
	isFake := fs.Bool("fake", false, "use the fake agent profile")
	wt := fs.Bool("worktree", false, "give the agent its own git worktree, whatever config.toml says")
	noWT := fs.Bool("no-worktree", false, "spawn straight into dir, with no worktree of its own")
	goal := fs.String("goal", "", "task card: the goal in one line")
	check := fs.String("check", "", "task card: done-check command, run in the agent's dir when it reports done")
	checkExit := fs.Int("check-exit", 0, "task card: exit code the done-check must return")
	result := fs.String("result", "", "task card: result file the agent writes, relative to its dir (default "+fleet.DefaultResult+", or result-<name>.md without a worktree of its own; result.md given explicitly is renamed the same way)")
	leader := fs.Bool("leader", false, "make it a leader: it may spawn a squad of its own and its agents report to it")
	interactive := fs.Bool("interactive", false, "the user talks to it directly: never auto-closed, reports progress until the user ends the session")
	effort := fs.String("effort", "", "worker playbook level: light, standard or pro (default: pro with a worktree, light without)")
	driver := fs.String("driver", "", "tmux or acp: how fleet runs the agent (default: driver in config.toml, else tmux)")
	owner := fs.String("admiral", "", "admiral that owns the agent and gets its reports (default main)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *owner != "" && *owner != supervisor.Main && !supervisor.Exists(f.Home, *owner) {
		return fmt.Errorf("no admiral %q (fleet admiral ls)", *owner)
	}
	var o fleet.SpawnOpts
	o.Role = fleet.Role{Leader: *leader, Interactive: *interactive, Admiral: *owner}
	o.Effort = *effort
	o.Driver = *driver
	o.Card = fleet.NormalizeCard(&board.Card{Goal: *goal, Check: *check, Want: *checkExit, Result: *result})
	var set []string
	// Read which flag was passed, not just its value, so --worktree=false and
	// --no-worktree=false mean what they say.
	fs.Visit(func(fl *flag.Flag) {
		switch fl.Name {
		case "worktree":
			set, o.Worktree = append(set, fl.Name), wt
		case "no-worktree":
			v := !*noWT
			set, o.Worktree = append(set, fl.Name), &v
		}
	})
	if len(set) > 1 {
		return fmt.Errorf("--worktree and --no-worktree are opposites; pass one")
	}
	a := fs.Args()
	var name, profile, dir, prompt string
	if *isFake {
		if len(a) < 1 {
			return fmt.Errorf("usage: fleet spawn --fake <name> [dir]")
		}
		name, profile = a[0], "fake"
		if len(a) > 1 {
			dir = a[1]
		}
	} else {
		if len(a) < 2 {
			return fmt.Errorf("usage: fleet spawn <name> <profile> [dir] [\"prompt\"]")
		}
		name, profile = a[0], a[1]
		if len(a) > 2 {
			dir = a[2]
		}
		if len(a) > 3 {
			prompt = strings.Join(a[3:], " ")
		}
	}
	// A leader that reaches for the CLI instead of fleet_squad_spawn used to
	// get a sibling: not in its squad, not in its team, reporting to the
	// admiral. Give it the squad member it meant (ADR-58).
	spawn := f.SpawnWith
	squadNote := ""
	me := f.Whoami("")
	if a, ok := f.Store.Load().Agents[me]; ok && me != "" && a.LeadBy != "" && *leader {
		// A top-level leader started from inside a squad would step
		// around max_depth (ADR-59).
		return fmt.Errorf("you are in %s's squad; your agents may not lead squads. Report to your leader instead", a.LeadBy)
	}
	if me != "" && f.IsLeader(me) {
		if *leader {
			return fmt.Errorf("you lead a squad; your agents may not lead squads of their own. Spawn with the fleet_squad_spawn tool")
		}
		spawn = func(o fleet.SpawnOpts, name, profile, dir, prompt string) (*board.Agent, error) {
			return f.SquadSpawn(me, o, name, profile, dir, prompt)
		}
		squadNote = "in your squad: it reports to you, not to the admiral. Spawn with the fleet_squad_spawn tool, not the CLI\n"
	}
	ag, err := spawn(o, name, profile, dir, prompt)
	if err != nil {
		return err
	}
	if ag.Status == string(status.Queued) {
		fmt.Printf("queued %s (%s): %d ahead of it, %d busy, max_working %d\n", ag.Name, f.SpawnLabel(ag), f.QueueAhead(ag.Name), f.Busy(), f.MaxWorking())
		if ag.LastLine != "" {
			fmt.Println(ag.LastLine)
		}
		return nil
	}
	fmt.Printf("spawned %s (%s) in %s pane %s%s\n", ag.Name, f.SpawnLabel(ag), ag.Dir, ag.PaneID, dirNote(f, ag.Dir))
	fmt.Print(squadNote)
	if w := ag.Worktree; w != nil {
		fmt.Printf("worktree %s on branch %s (of %s)\n", w.Path, w.Branch, w.Repo)
	}
	if ag.Leader {
		fmt.Printf("leader: it has squad tools, and its agents' reports go to it\n")
	}
	if c := ag.Card; c != nil {
		fmt.Printf("task card: result file %s\n", c.Result)
		if c.Check != "" {
			fmt.Printf("done-check on report done: %s (want exit %d)\n", c.Check, c.Want)
		}
	}
	return nil
}

// leaderScope keeps the CLI run from inside a leader's pane to that
// leader's own squad, the same scope its fleet_squad_* tools have: it may
// close, send to, park and wake its members and nobody else, and it may not
// type into a member's prompt (ADR-53, ADR-59). Outside an agent pane, or
// in a plain agent's, nothing changes.
func leaderScope(f *fleet.Fleet, cmd string, rest []string) error {
	switch cmd {
	case "close", "kill", "send", "park", "wake":
	default:
		return nil
	}
	me := f.Whoami("")
	if me == "" || !f.IsLeader(me) {
		return nil
	}
	// The target is the first non-flag argument: none of these commands
	// has a flag that takes a value. One that does must be skipped here.
	target := ""
	for _, a := range rest {
		if !strings.HasPrefix(a, "-") {
			target = a
			break
		}
	}
	if target == "" {
		return nil // the command's own usage error says what is missing
	}
	m, err := f.SquadMember(me, target)
	if err != nil {
		return fmt.Errorf("you lead a squad, so fleet %s acts only on your own agents: %w", cmd, err)
	}
	if cmd == "send" && m.Status == string(status.Waiting) {
		return fmt.Errorf("%s is on a prompt; report it to the admiral, you may not answer prompts", target)
	}
	return nil
}

func cmdAdopt(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("adopt", flag.ContinueOnError)
	name := fs.String("name", "", "agent name (default: base name of dir)")
	dir := fs.String("dir", "", "working dir (default: the session's)")
	force := fs.Bool("force", false, "skip the session-exists and in-use checks")
	driver := fs.String("driver", "", "tmux or acp: how fleet runs the agent (default as for spawn)")
	a, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(a) == 0 {
		return fmt.Errorf("usage: fleet adopt [--name N] [--dir D] [--force] [--driver tmux|acp] [profile] <id | words...>")
	}
	ag, err := f.AdoptArgs(fleet.AdoptOpts{Name: *name, Dir: *dir, Force: *force, Driver: *driver}, a)
	if err != nil {
		return err
	}
	fmt.Printf("adopted %s (%s) session %s in %s pane %s%s\n", ag.Name, ag.Profile, ag.SessionID, ag.Dir, ag.PaneID, dirNote(f, ag.Dir))
	return nil
}

func cmdSessions(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("sessions", flag.ContinueOnError)
	all := fs.Bool("all", false, "include sessions with no prompt and ones on the board")
	asJSON := fs.Bool("json", false, "JSON output")
	n := fs.Int("n", 20, "max sessions (0 = all)")
	words, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	ss, err := f.Sessions(sessions.Opts{Words: words, All: *all, Limit: *n})
	if err != nil {
		return err
	}
	if *asJSON {
		e := json.NewEncoder(os.Stdout)
		e.SetIndent("", "  ")
		if ss == nil {
			ss = []sessions.Session{}
		}
		return e.Encode(ss)
	}
	home, _ := os.UserHomeDir()
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "AGE\tPROFILE\tDIR\tTITLE\tID\tFLAGS")
	for _, s := range ss {
		d := s.Dir
		if home != "" && (d == home || strings.HasPrefix(d, home+"/")) {
			d = "~" + d[len(home):]
		}
		if r := []rune(d); len(r) > 40 {
			d = "…" + string(r[len(r)-39:])
		}
		var flags []string
		if s.InUse {
			flags = append(flags, "in use")
		}
		if s.OnBoard {
			flags = append(flags, "on board")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", sessions.Age(time.Since(s.Updated)), s.Profile, d, sessions.OneLine(s.Title, 60), sessions.Short(s.ID), strings.Join(flags, ", "))
	}
	return w.Flush()
}

// parseInterleaved parses flags that may come before, between or after positionals.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos, args = append(pos, fs.Arg(0)), fs.Args()[1:]
	}
}

func cmdLs(f *fleet.Fleet, args []string) error {
	// `fleet ls` drains the queue and runs the memory guard too, so neither
	// waits for the overview TUI (§7, §7a). The launch runs in this
	// process: a goroutine would die with it.
	f.DrainQueue, f.DrainSync = true, true
	as, err := f.Refresh()
	if err != nil {
		return err
	}
	return writeLs(os.Stdout, as, f.Teams.ByAgent(), len(args) > 0 && args[0] == "--json")
}

// lsAgent adds team fields to the JSON of an agent without touching board.Agent.
type lsAgent struct {
	*board.Agent
	Team      string `json:"team,omitempty"`
	TeamColor string `json:"team_color,omitempty"`
}

// MarshalJSON splices the team fields onto the agent's own JSON. board.Agent
// marshals itself (ADR-58), and an embedded type's marshaller would otherwise
// be promoted and swallow these two fields.
func (l lsAgent) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal(l.Agent)
	if err != nil {
		return nil, err
	}
	var extra []byte
	for _, kv := range [][2]string{{"team", l.Team}, {"team_color", l.TeamColor}} {
		if kv[1] == "" {
			continue
		}
		v, err := json.Marshal(kv[1])
		if err != nil {
			return nil, err
		}
		extra = append(append(extra, `,"`+kv[0]+`":`...), v...)
	}
	if len(extra) == 0 || len(b) < 2 || b[len(b)-1] != '}' {
		return b, nil
	}
	return append(append(b[:len(b)-1:len(b)-1], extra...), '}'), nil
}

// writeLs prints the agent table. The TEAM column appears only when a listed
// agent has a team, so the output without teams is unchanged.
func writeLs(out io.Writer, as []*board.Agent, teams map[string]team.Info, asJSON bool) error {
	if asJSON {
		e := json.NewEncoder(out)
		e.SetIndent("", "  ")
		if len(teams) == 0 {
			return e.Encode(as)
		}
		ls := make([]lsAgent, len(as))
		for i, a := range as {
			ls[i] = lsAgent{Agent: a, Team: teams[a.Name].Team, TeamColor: teams[a.Name].Color}
		}
		return e.Encode(ls)
	}
	withTeam := false
	for _, a := range as {
		if teams[a.Name].Team != "" {
			withTeam = true
		}
	}
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 2, 2, ' ', 0)
	if withTeam {
		fmt.Fprintln(w, "NAME\tTEAM\tPROFILE\tDRIVER\tSTATUS\tFOR\tPANE\tLAST LINE")
	} else {
		fmt.Fprintln(w, "NAME\tPROFILE\tDRIVER\tSTATUS\tFOR\tPANE\tLAST LINE")
	}
	colors := []string{""}
	for _, a := range as {
		ll := a.LastLine
		if len(ll) > 60 {
			ll = ll[:60]
		}
		name := a.Name
		if withTeam {
			ti := teams[a.Name]
			cell := ""
			if ti.Team != "" {
				cell = teamMark + " " + ti.Team
			}
			name += "\t" + cell
			colors = append(colors, ti.Color)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, a.Profile, a.DriverName(), a.Status, time.Since(a.StatusSince).Round(time.Second), a.PaneID, ll)
	}
	w.Flush()
	table := buf.String()
	if withTeam {
		table = colourMarks(table, strings.Index(table, "TEAM"), colors)
	}
	_, err := io.WriteString(out, table)
	return err
}

func cmdPeek(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("peek", flag.ContinueOnError)
	fs.Bool("raw", true, "raw pane text (summaries come in slice 7)")
	n := fs.Int("n", 0, "lines of history")
	live := fs.Bool("live", false, "live colour view of the pane (the preview popup)")
	sum := fs.Bool("summary", false, "about 5 lines from a cheap model instead of the pane text")
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if name == "" && fs.NArg() > 0 {
		name = fs.Arg(0)
	}
	if name == "" {
		return fmt.Errorf("usage: fleet peek <name> [--raw] [-n N] | fleet peek --live <name> | fleet peek --summary <name>")
	}
	if *live {
		return cmdPeekLive(f, name)
	}
	if *sum {
		return cmdPeekSummary(f, name)
	}
	out, err := f.Peek(name, *n)
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

// dirNote tells the user when fleet did not start the agent where it was
// asked to: a home-directory spawn goes to a scratch dir of its own (ADR-47).
func dirNote(f *fleet.Fleet, dir string) string {
	if !f.InScratch(dir) {
		return ""
	}
	return "\n  " + fleet.RedirectNote()
}
