package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
	"github.com/sturlabragason/fleet/internal/page"
	"github.com/sturlabragason/fleet/internal/pages"
)

const pageUsage = "usage: fleet page check <brief.json> | fleet page render <brief.json> <dir> | fleet page publish [--replace] <brief.json>"

// cmdPage runs "fleet page check|render|publish" (ADR-197): the executive
// page format, from a brief an agent writes to a published page.
func cmdPage(args []string) error {
	if len(args) == 0 {
		return errors.New(pageUsage)
	}
	sub := args[0]
	fs := flag.NewFlagSet("page "+sub, flag.ContinueOnError)
	replace := fs.Bool("replace", false, "publish over an existing artifact of the same name")
	pos, err := parseInterleaved(fs, args[1:])
	if err != nil {
		return err
	}
	want := map[string]int{"check": 1, "render": 2, "publish": 1}[sub]
	if want == 0 || len(pos) != want {
		return errors.New(pageUsage)
	}
	b, err := page.Load(pos[0])
	if err != nil {
		return err
	}
	if p := page.Check(b); len(p) > 0 {
		return fmt.Errorf("%s does not meet the page format (ADR-197):\n  %s", pos[0], strings.Join(p, "\n  "))
	}
	switch sub {
	case "check":
		fmt.Println("ok")
		return nil
	case "render":
		return page.Write(b, pos[1])
	}
	return publishPage(context.Background(), b, *replace)
}

// publishPage renders b into a temp dir and publishes it with the artifact
// companion, refusing to overwrite another page unless replace is set. It
// warns when the replaced version is open unanswered, since the publish
// voids that tab's Submit, and says how long Submit stays open (ADR-199).
func publishPage(ctx context.Context, b page.Brief, replace bool) error {
	t, err := companion.Find(pages.Spec)
	if err != nil {
		return fmt.Errorf("find artifact: %w", err)
	}
	list, err := t.Run(ctx, companion.Opts{}, "list")
	switch {
	case err != nil && !replace:
		return fmt.Errorf("list artifacts: %w", err)
	case err != nil:
		fmt.Fprintf(os.Stderr, "warning: could not list artifacts (%v); not checking who has %s open\n", err, b.Page)
	}
	if published, ok := pages.Published(string(list), b.Page); ok {
		if !replace {
			return fmt.Errorf("artifact %s exists: pass --replace to publish over it, or bump round to ask again", b.Page)
		}
		warnOpenTab(ctx, t, b.Page, published)
	}
	dir, err := os.MkdirTemp("", "fleet-page-")
	if err != nil {
		return fmt.Errorf("make temp dir: %w", err)
	}
	defer os.RemoveAll(dir)
	if err := page.Write(b, dir); err != nil {
		return err
	}
	out, err := t.Run(ctx, companion.Opts{Timeout: 2 * time.Minute}, "publish", b.Page, dir)
	if err != nil {
		return fmt.Errorf("publish %s: %w", b.Page, err)
	}
	fmt.Print(string(out))
	if page.Asks(b) {
		fmt.Println("Submit stays open until this page is republished or deleted; there is no timer. An answer no agent takes within a minute goes to the admiral.")
	}
	return nil
}

// warnOpenTab tells the publisher when someone opened the live version of
// name and sent no answer: they may be filling it in.
func warnOpenTab(ctx context.Context, t companion.Tool, name string, published time.Time) {
	log, err := t.Run(ctx, companion.Opts{}, "access-log", name, "--since", "7d")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not read who has %s open (%v); a republish voids the Submit of an open tab\n", name, err)
		return
	}
	if who, at, ok := pages.Unanswered(string(log), name, published); ok {
		fmt.Fprintf(os.Stderr, "warning: %s opened %s at %s UTC and has sent no answer. This publish voids the Submit of that open tab: within 30 seconds the page says so, and what they typed comes back when they reload.\n",
			who, name, at.Format("15:04"))
	}
}
