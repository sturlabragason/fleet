package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/tools"
	"github.com/sturlabragason/fleet/internal/wall"
)

// Several admirals on one fleet (DESIGN §6.12, ADR-64): the `fleet admiral
// add|ls|close|give` verbs and the plumbing that starts their panes.

// AdmiralEnv carries a non-main admiral's name to its fleet MCP server, for
// profiles whose MCP config is a file (copilot's supervisor/.mcp.json).
const AdmiralEnv = "FLEET_ADMIRAL_NAME"

// admiralLabel is how messages name an admiral: "admiral" for main.
func admiralLabel(adm string) string {
	if adm == supervisor.Main || adm == "" {
		return "admiral"
	}
	return "admiral " + adm
}

// nameFlag takes `--name N` out of args. No flag means main; a name that is
// not an admiral of this fleet is an error.
func nameFlag(f *fleet.Fleet, args []string) (string, []string, error) {
	adm, rest := supervisor.Main, args[:0:0]
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--name" && i+1 < len(args):
			adm = args[i+1]
			i++
		case strings.HasPrefix(a, "--name="):
			adm = strings.TrimPrefix(a, "--name=")
		default:
			rest = append(rest, a)
		}
	}
	if adm != supervisor.Main && !supervisor.Exists(f.Home, adm) {
		return "", nil, fmt.Errorf("no admiral %q (fleet admiral ls)", adm)
	}
	return adm, rest, nil
}

// newAdmiralPane starts the pane of a registered non-main admiral beside the
// main admiral and records its session.
func newAdmiralPane(f *fleet.Fleet, adm, prof string) (string, error) {
	sl, err := supervisorCmdOf(f, adm, prof, "")
	if err != nil {
		return "", err
	}
	target := mainTarget()
	if p, err := supervisor.FindPane(f.T, target); err == nil {
		target = p
	}
	a := []string{"split-window", "-d", "-v", "-t", target, "-P", "-F", "#{pane_id}", "-c", sl.Dir}
	for _, e := range sl.Env {
		a = append(a, "-e", e)
	}
	pane, err := f.T.Run(append(a, sl.Cmd)...)
	if err != nil {
		return "", err
	}
	setAdmiralPaneOptsOf(f, adm, pane, sl.Profile)
	if err := supervisor.SaveOf(f.Home, adm, supervisor.Session{SessionID: sl.ID, Profile: sl.Profile, PaneID: pane, Started: time.Now(), Dir: sl.Dir, Generation: 1}); err != nil {
		logx.For("admiral").Error("save admiral session", "admiral", adm, "err", err)
	}
	logx.For("admiral").Info("admiral pane", "admiral", adm, "profile", sl.Profile, "pane", pane)
	return pane, nil
}

// ensureAdmirals gives every registered admiral a live pane (fleet up).
func ensureAdmirals(f *fleet.Fleet) {
	panes, _ := f.T.ListPanes("")
	have := map[string]bool{}
	for _, p := range panes {
		if p.Role == "sup" && p.Session == fleet.Session {
			have[p.Admiral] = true
		}
	}
	for _, a := range supervisor.Admirals(f.Home) {
		if have[a.Name] {
			continue
		}
		if _, err := newAdmiralPane(f, a.Name, a.Profile); err != nil {
			logx.For("admiral").Error("start admiral", "admiral", a.Name, "err", err)
			fmt.Fprintf(os.Stderr, "fleet: admiral %s: %v\n", a.Name, err)
		}
	}
}

func cmdAdmiralAdd(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("admiral add", flag.ContinueOnError)
	prof := fs.String("profile", "", "tool profile (default: main's)")
	if err := fs.Parse(reorderFlags(args)); err != nil || fs.NArg() != 1 {
		return fmt.Errorf("usage: fleet admiral add NAME [--profile P]")
	}
	name := fs.Arg(0)
	if !f.T.HasSession(fleet.Session) {
		return fmt.Errorf("fleet is not up; run fleet up")
	}
	if *prof == "" {
		*prof = resolveAdmiralOf(f, supervisor.Main)
	}
	if _, ok := f.Profiles[*prof]; !ok {
		return fmt.Errorf("no profile %q (have %s)", *prof, strings.Join(tools.Names(f.Profiles), ", "))
	}
	if _, err := supervisor.Add(f.Home, name, *prof, f.Prefs.AdmiralMax()); err != nil {
		return err
	}
	if _, err := newAdmiralPane(f, name, *prof); err != nil {
		supervisor.Remove(f.Home, name)
		return err
	}
	if err := wall.New(f.T, fleet.Session).Apply(); err != nil {
		logx.For("admiral").Error("apply", "err", err)
	}
	msg := fmt.Sprintf("admiral %s started on %s", name, *prof)
	display(f, msg)
	fmt.Println(msg)
	return nil
}

// reorderFlags moves flags before positionals so `add NAME --profile P` parses.
func reorderFlags(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flags = append(flags, args[i])
			if !strings.Contains(args[i], "=") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		} else {
			pos = append(pos, args[i])
		}
	}
	return append(flags, pos...)
}

func cmdAdmiralLs(f *fleet.Fleet) error {
	by := f.UnreadBy()
	owned := map[string]int{}
	for _, a := range f.Store.Load().Agents {
		owned[a.Owner()]++
	}
	for _, n := range f.AdmiralNames() {
		prof := f.Prefs.AdmiralProfile()
		if s := supervisor.CurrentOf(f.Home, f.T, mainTarget(), n); s.Profile != "" {
			prof = s.Profile
		}
		fmt.Printf("%-16s %-18s %d agents · %d unread\n", n, prof, owned[n], by[n])
	}
	return nil
}

func cmdAdmiralClose(f *fleet.Fleet, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: fleet admiral close NAME")
	}
	name := args[0]
	pane, perr := supervisor.FindPaneOf(f.T, mainTarget(), name)
	if perr != nil { // a hidden pane lives in another window
		panes, _ := f.T.ListPanes("")
		for _, p := range panes {
			if p.Role == "sup" && p.Admiral == name {
				pane, perr = p.ID, nil
			}
		}
	}
	agents, reports, err := f.CloseAdmiral(name)
	if err != nil {
		return err
	}
	if perr == nil {
		f.KillPane(pane)
	}
	wall.New(f.T, fleet.Session).Apply()
	msg := fmt.Sprintf("admiral %s closed · %d agents and %d unread reports → main", name, agents, reports)
	display(f, msg)
	fmt.Println(msg)
	return nil
}

// cmdAdmiralMin: `fleet admiral min NAME` minimizes an admiral pane in the
// admiral column, or gives a minimized one an even share back (ADR-84).
func cmdAdmiralMin(f *fleet.Fleet, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: fleet admiral min NAME")
	}
	min, err := wall.New(f.T, fleet.Session).MinimizeAdmiral(args[0])
	if err != nil {
		return err
	}
	msg := "admiral " + args[0] + " restored"
	if min {
		msg = "admiral " + args[0] + " minimized · fleet admiral min " + args[0] + " restores"
	}
	display(f, msg)
	fmt.Println(msg)
	return nil
}

// cmdAdmiralGive: `fleet admiral give AGENT --to NAME` (the user's hand-over).
func cmdAdmiralGive(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("admiral give", flag.ContinueOnError)
	to := fs.String("to", "", "admiral that takes the agent")
	if err := fs.Parse(reorderFlags(args)); err != nil || fs.NArg() != 1 {
		return fmt.Errorf("usage: fleet admiral give AGENT [--to NAME]")
	}
	if *to == "" { // no target: ask in a menu (the overview's O and right-click)
		return ownerMenu(f, fs.Arg(0))
	}
	n, err := f.Give("", fs.Arg(0), *to)
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("→ %s · %s (%d unread reports moved)", *to, fs.Arg(0), n)
	display(f, msg)
	fmt.Println(msg)
	return nil
}

// cmdAdmiralAct is behind M-R and M-P: `fleet admiral act reset|pick --pane %N`.
// Focused on an admiral pane it acts on that admiral; with one admiral it
// acts on main; otherwise it asks which, never guessing main (DESIGN §6.12).
func cmdAdmiralAct(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("admiral act", flag.ContinueOnError)
	pane := fs.String("pane", "", "focused pane id")
	if err := fs.Parse(reorderFlags(args)); err != nil || fs.NArg() != 1 {
		return fmt.Errorf("usage: fleet admiral act reset|pick [--pane %%N]")
	}
	verb := fs.Arg(0)
	if verb != "reset" && verb != "pick" {
		return fmt.Errorf("unknown verb %q", verb)
	}
	run := func(adm string) error {
		if verb == "reset" {
			_, err := f.T.Run(confirmResetArgsOf(f.Exe, adm)...)
			return err
		}
		_, err := f.T.Run(pickPopupArgsOf(f, adm)...)
		return err
	}
	names := f.AdmiralNames()
	if len(names) == 1 {
		return run(supervisor.Main)
	}
	for _, p := range func() []string {
		ps, _ := f.T.ListPanes("")
		var out []string
		for _, q := range ps {
			if q.ID == *pane && q.Role == "sup" {
				a := q.Admiral
				if a == "" {
					a = supervisor.Main
				}
				out = append(out, a)
			}
		}
		return out
	}() {
		return run(p)
	}
	return admiralMenu(f, verb, names)
}

// admiralMenu asks which admiral a key press means, in a tmux menu.
func admiralMenu(f *fleet.Fleet, verb string, names []string) error {
	a := []string{"display-menu", "-T", " " + verb + " which admiral? "}
	for i, n := range names {
		var cmd string
		if verb == "reset" {
			cmd = tmuxCmdString(confirmResetArgsOf(f.Exe, n))
		} else {
			cmd = tmuxCmdString(pickPopupArgsOf(f, n))
		}
		key := ""
		if i < 9 {
			key = fmt.Sprint(i + 1)
		}
		a = append(a, n, key, cmd)
	}
	_, err := f.T.Run(a...)
	return err
}

// ownerMenu asks which admiral takes agent, in a tmux menu of the others.
func ownerMenu(f *fleet.Fleet, agent string) error {
	cur := f.OwnerOf(agent)
	a := []string{"display-menu", "-T", " hand " + agent + " to "}
	n := 0
	for _, ad := range f.AdmiralNames() {
		if ad == cur {
			continue
		}
		n++
		prof := f.Prefs.AdmiralProfile()
		if s := supervisor.CurrentOf(f.Home, f.T, mainTarget(), ad); s.Profile != "" {
			prof = s.Profile
		}
		cmd := tmuxCmdString([]string{"run-shell", "-b", tools.Quote(f.Exe) + " admiral give " + tools.Quote(agent) + " --to " + tools.Quote(ad) + " >/dev/null 2>&1"})
		a = append(a, "● "+ad+" · "+prof, fmt.Sprint(n), cmd)
	}
	if n == 0 {
		display(f, "only one admiral · fleet admiral add <name>")
		return nil
	}
	_, err := f.T.Run(a...)
	return err
}
