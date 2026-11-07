package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/mcp"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/wall"
)

const mailUsage = `usage: fleet mail [--as NAME] agents | inbox [--all] [-n N] | send <to|*> "text" | request <to> ["reason"] | grant <who> | deny <who> | revoke <who>`

// cmdMail is the agent side of DESIGN §6.6 for tools without MCP.
func cmdMail(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("mail", flag.ContinueOnError)
	as := fs.String("as", "", "act as this agent (default: the fleet pane you run in)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("%s", mailUsage)
	}
	sub, rest := fs.Arg(0), fs.Args()[1:]
	me, err := f.Me(*as)
	if err != nil {
		return err
	}
	m := f.Mail()
	one := func() (string, error) {
		if len(rest) != 1 {
			return "", fmt.Errorf("usage: fleet mail %s <who>", sub)
		}
		return rest[0], nil
	}
	switch sub {
	case "agents":
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tPROFILE\tSTATUS\tACCESS\tDIR")
		for _, p := range f.MailAgents(me) {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", p.Name, p.Profile, p.Status, p.Access, p.Dir)
		}
		return w.Flush()
	case "inbox":
		ifs := flag.NewFlagSet("inbox", flag.ContinueOnError)
		all := ifs.Bool("all", false, "show the last N messages, read or not")
		n := ifs.Int("n", 20, "N for --all")
		if err := ifs.Parse(rest); err != nil {
			return err
		}
		ms, err := m.Inbox(me, *all, *n)
		if err != nil {
			return err
		}
		fmt.Println(mcp.RenderInbox(ms, *all))
		return nil
	case "send":
		if len(rest) < 2 {
			return fmt.Errorf("usage: fleet mail send <to|*> \"text\"")
		}
		rcpt, err := f.MailSend(me, rest[0], strings.Join(rest[1:], " "))
		if err != nil {
			return err
		}
		fmt.Println("sent to " + strings.Join(rcpt, ", "))
		return nil
	case "request":
		if len(rest) < 1 {
			return fmt.Errorf("usage: fleet mail request <to> [\"reason\"]")
		}
		st, err := f.MailRequest(me, rest[0], strings.Join(rest[1:], " "))
		if err != nil {
			return err
		}
		fmt.Printf("access to %s: %s\n", rest[0], st)
		return nil
	case "grant", "deny":
		who, err := one()
		if err != nil {
			return err
		}
		return m.Respond(me, who, sub == "grant")
	case "revoke":
		who, err := one()
		if err != nil {
			return err
		}
		return m.Revoke(me, who)
	}
	return fmt.Errorf("unknown mail command %q\n%s", sub, mailUsage)
}

// cmdReport is fleet_report for agents without MCP (DESIGN §6.7).
func cmdReport(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	as := fs.String("as", "", "act as this agent (default: the fleet pane you run in)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: fleet report [--as NAME] <done|blocked|question|progress> \"summary\"")
	}
	me, err := f.Me(*as)
	if err != nil {
		return err
	}
	state := fs.Arg(0)
	note, err := f.Report(me, state, strings.Join(fs.Args()[1:], " "))
	if err != nil {
		return err
	}
	out := "reported " + state + " to the admiral"
	if note != "" {
		out += " (" + note + ")"
	}
	fmt.Println(out)
	return nil
}

const reportsUsage = `usage: fleet reports [--all] [--limit N] [--as NAME]`

// cmdReports is fleet_reports for agents and the user without MCP
// (DESIGN §6.7): the same mailbox and rendering.
func cmdReports(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("reports", flag.ContinueOnError)
	all := fs.Bool("all", false, "show the last N reports, read or not, without marking them read")
	limit := 20
	fs.IntVar(&limit, "limit", limit, "N for --all")
	fs.IntVar(&limit, "n", limit, "alias for --limit")
	as := fs.String("as", "", "read as this agent (default: the fleet pane you run in, else the admiral)")
	adm := fs.String("admiral", "", "read this admiral's reports (default main)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *adm != "" && *adm != supervisor.Main && !supervisor.Exists(f.Home, *adm) {
		return fmt.Errorf("no admiral %q (fleet admiral ls)", *adm)
	}
	admBox = mail.BoxOf(*adm)
	if fs.NArg() != 0 {
		return fmt.Errorf("%s", reportsUsage)
	}
	defer func() { admBox = mail.Admiral }()
	return writeReports(os.Stdout, f, *as, *all, limit)
}

// admBox is the admiral mailbox `fleet reports` reads (main unless --admiral).
var admBox = mail.Admiral

// writeReports renders the admiral's report mailbox, the one place reports
// land. Only the admiral itself — a shell outside every agent pane and
// without --as — marks them read, the way fleet_reports does; an agent
// reading the history must not empty the admiral's unread queue, so it
// peeks. --all never marks anything.
func writeReports(w io.Writer, f *fleet.Fleet, as string, all bool, limit int) error {
	me, err := f.Identity("")
	if as != "" {
		me, err = f.Me(as) // validates the name against the board
	}
	if err != nil {
		return err
	}
	// A leader's squad reports land in its own mailbox, so that is the box
	// it reads; everyone else reads the admiral's (ADR-53).
	box := admBox
	if me != "" && f.IsLeader(me) {
		box = me
	}
	var ms []mail.Msg
	if all || me == "" {
		if ms, err = f.Mail().Inbox(box, all, limit); err != nil {
			return err
		}
	} else if box == me {
		ms = reportsOnly(f.Mail().Peek(box))
	} else {
		ms = f.Mail().Peek(box)
	}
	_, err = fmt.Fprintln(w, mcp.RenderReports(ms, all))
	return err
}

// reportsOnly keeps the report messages of a mailbox: a leader's inbox
// holds ordinary peer mail too.
func reportsOnly(ms []mail.Msg) []mail.Msg {
	var out []mail.Msg
	for _, m := range ms {
		if m.Kind == mail.KindReport {
			out = append(out, m)
		}
	}
	return out
}

// cmdAccess is the user's view and decisions on agent access pairs.
func cmdAccess(f *fleet.Fleet, args []string) error {
	if len(args) == 0 || args[0] == "ls" {
		fmt.Println(mcp.PolicyLine(f.MailPolicy()))
		fmt.Println(strings.TrimRight(mcp.RenderPairs(f.Mail().All()), "\n"))
		return nil
	}
	switch args[0] {
	case "log":
		n := 50
		if len(args) == 3 && args[1] == "-n" {
			v, err := strconv.Atoi(args[2])
			if err != nil || v <= 0 {
				return fmt.Errorf("usage: fleet access log [-n N]")
			}
			n = v
		} else if len(args) != 1 {
			return fmt.Errorf("usage: fleet access log [-n N]")
		}
		fmt.Println(strings.TrimRight(mcp.RenderLog(f.Mail().Log(n)), "\n"))
		return nil
	case "grant", "deny", "revoke":
		if len(args) != 3 {
			return fmt.Errorf("usage: fleet access %s A B", args[0])
		}
		if err := f.UserOnly(); err != nil {
			return err
		}
		return f.AccessDecide(args[1], args[2], args[0])
	}
	return fmt.Errorf("usage: fleet access [ls] | log [-n N] | grant A B | deny A B | revoke A B")
}

// cmdMCP serves the admiral tools, or the agent tools with --agent NAME or
// when run inside an agent pane.
func cmdMCP(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	agent := fs.String("agent", "", "serve the agent-scoped peer tools as this agent")
	adm := fs.String("admiral", "", "serve the admiral tools as this admiral (default: main)")
	probe := fs.Bool("probe", false, "check this binary can serve, print ok and exit (used by hot reload)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *probe {
		if err := mcp.ProbeCheck(); err != nil {
			return err
		}
		fmt.Println("ok")
		return nil
	}
	me, err := f.Identity(*agent)
	if err != nil {
		return err
	}
	// A done-check must never stall the MCP loop (ADR-43).
	f.Async()
	in, reloaded := mcp.ResumeInput(os.Stdin)
	name := resolveAdmiralName(*adm, os.Getenv, func() string { return paneAdmiral(f) })
	if me == "" {
		if name, err = checkAdmiral(f.Home, name); err != nil {
			return err
		}
	}
	s := mcp.NewAdmiral(f, wall.New(f.T, fleet.Session), name)
	if me != "" {
		s = mcp.NewAgent(f, me)
	} else {
		// The admiral's server drains the spawn queue and runs the memory
		// guard, so neither needs the overview TUI to be running (§7, §7a).
		f.DrainQueue = true
	}
	s.Reloaded = reloaded
	return s.Serve(in, os.Stdout)
}

// resolveAdmiralName names the admiral a `fleet mcp` serves: --admiral, then
// FLEET_ADMIRAL_NAME, then the identity of its own pane (so a copilot
// admiral still knows who it is). "" means unknown: there is no fallback to
// main (DESIGN §6.12).
func resolveAdmiralName(flagVal string, getenv func(string) string, pane func() string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := getenv(AdmiralEnv); v != "" {
		return v
	}
	if pane != nil {
		return pane()
	}
	return ""
}

// checkAdmiral turns a resolved name into what the MCP server is built with:
// a registered admiral (main included), mcp.Unknown for "", or an error.
func checkAdmiral(home, name string) (string, error) {
	if name == "" {
		return mcp.Unknown, nil
	}
	if name != supervisor.Main && !supervisor.Exists(home, name) {
		if !supervisor.RegistryReadable(home) {
			// A torn registry must not kill a running admiral's server:
			// serve reads, refuse changes (DESIGN §6.12).
			logx.For("mcp").Error("admiral registry unreadable; identity unknown", "admiral", name)
			return mcp.Unknown, nil
		}
		return "", fmt.Errorf("fleet mcp: unknown admiral %q (fleet admiral ls)", name)
	}
	return name, nil
}

// paneAdmiral reads the identity of $TMUX_PANE on the fleet socket.
func paneAdmiral(f *fleet.Fleet) string {
	return paneAdmiralOf(os.Getenv("TMUX_PANE"), os.Getenv("TMUX"), f.T.SocketPath, f.T.PaneOption)
}

// paneAdmiralOf is paneAdmiral with tmux stubbed: the @fleet_admiral tag of an
// admiral pane (@role=sup), "main" for a sup pane without a tag (an admiral
// started before several admirals existed), "" (unknown) for anything else:
// no pane, another tmux server, pane not found or not an admiral.
func paneAdmiralOf(pane, tmuxEnv string, socket func() string, opt func(pane, key string) string) string {
	if pane == "" {
		return ""
	}
	if sock, _, _ := strings.Cut(tmuxEnv, ","); sock == "" || sock != socket() {
		return ""
	}
	if opt(pane, "@role") != "sup" {
		return ""
	}
	if tag := opt(pane, "@fleet_admiral"); tag != "" {
		return tag
	}
	return supervisor.Main
}
