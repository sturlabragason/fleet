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
// companion, refusing to overwrite another page unless replace is set.
func publishPage(ctx context.Context, b page.Brief, replace bool) error {
	t, err := companion.Find(pages.Spec)
	if err != nil {
		return fmt.Errorf("find artifact: %w", err)
	}
	if !replace {
		list, err := t.Run(ctx, companion.Opts{}, "list")
		if err != nil {
			return fmt.Errorf("list artifacts: %w", err)
		}
		for l := range strings.SplitSeq(string(list), "\n") {
			if name, _, _ := strings.Cut(l, "\t"); name == b.Page {
				return fmt.Errorf("artifact %s exists: pass --replace to publish over it, or bump round to ask again", b.Page)
			}
		}
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
	return nil
}
