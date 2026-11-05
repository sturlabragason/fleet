package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/mcp"
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
	s := mcp.New(f, wall.New(f.T, fleet.Session))
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
