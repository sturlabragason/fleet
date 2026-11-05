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
	"github.com/sturlabragason/fleet/internal/team"
	"github.com/sturlabragason/fleet/internal/tools"
)

const usage = `usage:
  fleet up                                  start/attach the fleet (admiral + overview + wall)
  fleet [--admiral-profile NAME] up [--no-attach]
                                            run the admiral under profile NAME (alone: fleet up); default
                                            $FLEET_ADMIRAL_PROFILE, last choice, [admiral] profile, copilot
  fleet ui                                  run the overview TUI in this terminal
  fleet spawn [--worktree|--no-worktree] <name> <profile> [dir] ["prompt"]
             [--goal "one line"] [--check "cmd"] [--check-exit N] [--result FILE]
                                            in a git repo the agent gets its own worktree,
                                            <repo>.wt/<name> on branch fleet/<name> (worktree in
                                            config.toml turns it off; --no-worktree for one spawn);
                                            --goal/--check/--result give it a task card: fleet runs
                                            the check in its dir when it reports done and tells the
                                            admiral pass or fail, and the agent is told to write the
                                            result file (default result.md);
                                            queued instead when max_working agents are busy;
                                            the overview TUI starts it when a slot frees
  fleet spawn --fake <name> [dir]           shorthand for profile "fake"
  fleet adopt [--name N] [--dir D] [--force] [profile] <id | words...>
                                            resume an existing session (started outside fleet) as an agent;
                                            by ID, unique ID prefix (8+ chars) or words from fleet sessions
  fleet sessions [--all] [--json] [-n 20] [words...]
                                            list copilot/claude sessions to adopt, newest first
  fleet ls [--json]
  fleet spend [--json]                      what each agent has cost so far and the fleet total, read from
                                            the agents' own session files (AI credits for copilot, tokens
                                            for claude); no model is called
  fleet send <name> "text"
  fleet peek <name> [--raw] [-n 200]
  fleet peek --summary <name>               about 5 lines from a cheap model: what it is doing, what it
                                            waits for, any error (cached per screen; [summary] in config.toml)
  fleet peek --live <name>                  live colour view of the agent's pane, 4x a second (Space in
                                            the overview opens it in a popup; Enter puts it in ▶)
  fleet close <name>                        stop an agent and remove it from the board
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
  fleet admiral [--json]                    admiral session, context fill, generation, uptime
  fleet admiral reset [--handoff]           replace the admiral with a fresh session (M-R)
  fleet admiral profile [NAME | --pick]     show the admiral's profile, or swap it (hands off; Alt-P picks)
  fleet admiral restart                     restart the admiral, resuming the same session
                                            ("fleet supervisor ..." still works: deprecated alias)
  fleet note [--agent NAME] "text"          append a note to ~/.fleet/decisions.log
  fleet playbook [dir]                      print the worker playbook prepended to a spawn prompt in dir
                                            (built-in + ~/.fleet/worker.md + the repo's .fleet/worker.md)
  fleet touching [-C dir] [--json] [file...]
                                            files other worktrees of this repo change; with files, only
                                            the overlaps (exit 0 none, 1 overlap, 2 error)
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
                                            (--keep-focus: show/add leave keyboard focus where it is;
                                            --notify: the result goes to a tmux message, not stdout)
  fleet keys [--markdown]                   list every hotkey (Alt-? in tmux, ? in the overview)
  fleet changes [--json]                    what's new in fleet: its latest features, newest first (Alt-g)

local preferences (never committed): ~/.config/fleet/config.toml ($FLEET_CONFIG overrides)
  sets the starting mode (e.g. copilot autopilot), extra args, prompt auto-submit,
  the worker playbook (playbook = false turns it off) and the per-agent git
  worktree (worktree = false turns it off);
  see examples/config.toml.example`

func run(args []string) error {
	args, err := topLevelProfile(args)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("%s", usage)
	}
	cmd, rest := args[0], args[1:]
	if cmd == "fakeagent" {
		name := "fake"
		if len(rest) > 0 {
			name = rest[0]
		}
		fake.Run(name, os.Stdin, os.Stdout)
		return nil
	}
	switch cmd { // need no fleet state or tmux
	case "keys":
		return cmdKeys(rest)
	case "playbook":
		return cmdPlaybook(rest)
	case "touching":
		return cmdTouching(rest)
	}
	if cmd == "changes" {
		return cmdChanges(rest)
	}
	f, err := fleet.Open()
	if err != nil {
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
	case "send":
		if len(rest) < 2 {
			return fmt.Errorf("usage: fleet send <name> \"text\"")
		}
		return f.Send(rest[0], strings.Join(rest[1:], " "))
	case "peek":
		return cmdPeek(f, rest)
	case "close", "kill": // "kill" kept as a hidden alias
		if len(rest) != 1 {
			return fmt.Errorf("usage: fleet close <name>")
		}
		return f.Close(rest[0])
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
	result := fs.String("result", "", "task card: result file the agent writes, relative to its dir (default "+fleet.DefaultResult+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var o fleet.SpawnOpts
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
	ag, err := f.SpawnWith(o, name, profile, dir, prompt)
	if err != nil {
		return err
	}
	if ag.Status == string(status.Queued) {
		fmt.Printf("queued %s (%d busy, max_working %d)\n", ag.Name, f.Busy(), f.Prefs.MaxWorking())
		return nil
	}
	fmt.Printf("spawned %s (%s) in %s pane %s\n", ag.Name, ag.Profile, ag.Dir, ag.PaneID)
	if w := ag.Worktree; w != nil {
		fmt.Printf("worktree %s on branch %s (of %s)\n", w.Path, w.Branch, w.Repo)
	}
	if c := ag.Card; c != nil {
		fmt.Printf("task card: result file %s\n", c.Result)
		if c.Check != "" {
			fmt.Printf("done-check on report done: %s (want exit %d)\n", c.Check, c.Want)
		}
	}
	return nil
}

func cmdAdopt(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("adopt", flag.ContinueOnError)
	name := fs.String("name", "", "agent name (default: base name of dir)")
	dir := fs.String("dir", "", "working dir (default: the session's)")
	force := fs.Bool("force", false, "skip the session-exists and in-use checks")
	a, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(a) == 0 {
		return fmt.Errorf("usage: fleet adopt [--name N] [--dir D] [--force] [profile] <id | words...>")
	}
	ag, err := f.AdoptArgs(fleet.AdoptOpts{Name: *name, Dir: *dir, Force: *force}, a)
	if err != nil {
		return err
	}
	fmt.Printf("adopted %s (%s) session %s in %s pane %s\n", ag.Name, ag.Profile, ag.SessionID, ag.Dir, ag.PaneID)
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
		fmt.Fprintln(w, "NAME\tTEAM\tPROFILE\tSTATUS\tFOR\tPANE\tLAST LINE")
	} else {
		fmt.Fprintln(w, "NAME\tPROFILE\tSTATUS\tFOR\tPANE\tLAST LINE")
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
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", name, a.Profile, a.Status, time.Since(a.StatusSince).Round(time.Second), a.PaneID, ll)
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
