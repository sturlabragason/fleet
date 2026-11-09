package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/mcp"
	"github.com/sturlabragason/fleet/internal/supervisor"
)

const waitUsage = `usage: fleet wait [--agents a,b] [--timeout 300s] [--as NAME] [--admiral NAME]`

// cmdWait is fleet_wait for a shell (ADR-72): block until a new report or
// message lands in the caller's mailbox. Exit 0 with the headlines, or 2
// on timeout or Ctrl-C.
func cmdWait(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	agents := fs.String("agents", "", "only wake for these agents, comma-separated")
	timeout := fs.Duration("timeout", mcp.WaitDefault, "how long to wait (max 10m)")
	as := fs.String("as", "", "wait as this agent (default: the fleet pane you run in, else the admiral)")
	adm := fs.String("admiral", "", "wait on this admiral's mailbox (default main; only from a shell outside every agent pane)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("%s", waitUsage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return runWait(ctx, os.Stdout, f, *as, *adm, splitList(*agents), *timeout)
}

func runWait(ctx context.Context, w io.Writer, f *fleet.Fleet, as, adm string, from []string, timeout time.Duration) error {
	d, err := mcp.WaitTimeout(timeout)
	if err != nil {
		return err
	}
	if adm != "" && adm != supervisor.Main && !supervisor.Exists(f.Home, adm) {
		return fmt.Errorf("no admiral %q (fleet admiral ls)", adm)
	}
	me, err := f.Identity("")
	if as != "" {
		me, err = f.Me(as)
	}
	if err != nil {
		return err
	}
	// An agent (a leader too) waits on its own mailbox; a shell outside
	// every agent pane waits on the admiral's.
	box, readWith := mail.BoxOf(adm), "fleet reports reads reports, fleet mail inbox other messages"
	if me != "" {
		box, readWith = me, "fleet mail inbox reads them"
	}
	if err := mcp.CheckSenders(f, from); err != nil {
		return err
	}
	start := time.Now()
	ms := mcp.Wait(ctx, f.Mail(), mcp.WaitOpts{Box: box, From: from, Timeout: d})
	fmt.Fprintln(w, mcp.RenderWait(ms, time.Since(start), readWith))
	if len(ms) == 0 {
		return exitCode(2)
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
