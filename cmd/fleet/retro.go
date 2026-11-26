package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/retro"
	"github.com/sturlabragason/fleet/internal/runs"
	"github.com/sturlabragason/fleet/internal/sysx"
)

// cmdRetro is fleet retro (ADR-204): the report, or its trial and promote
// subcommands.
func cmdRetro(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "trial":
			return retroTrial(os.Stdout, args[1:])
		case "promote":
			return retroPromote(os.Stdout, args[1:])
		}
	}
	fs := flag.NewFlagSet("retro", flag.ContinueOnError)
	since := fs.String("since", "30d", "window for the playbook table, rules and retro themes: a duration (7d) or a date")
	asJSON := fs.Bool("json", false, "machine-readable report")
	if err := fs.Parse(args); err != nil {
		return err
	}
	from, err := parseSince(*since, time.Now())
	if err != nil {
		return err
	}
	rep, err := retro.Build(logx.Home(), from)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(rep)
	}
	return retro.Write(os.Stdout, rep)
}

// humanOnly refuses a playbook change from inside an agent's process tree:
// a human starts and promotes a trial, never an agent (ADR-204).
func humanOnly(what string) error {
	if os.Getenv(sysx.OwnerEnv) != "" {
		return fmt.Errorf("fleet retro %s: run by a human in their own terminal, not by an agent (%s is set; ADR-204)", what, sysx.OwnerEnv)
	}
	return nil
}

func retroTrial(out io.Writer, args []string) error {
	fs := flag.NewFlagSet("retro trial", flag.ContinueOnError)
	stop := fs.Bool("stop", false, "end the running trial")
	share := fs.Int("share", retro.DefaultShare, "percent of new spawns that get the variant")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if err := humanOnly("trial"); err != nil {
		return err
	}
	home := logx.Home()
	if *stop {
		if len(pos) != 0 {
			return errors.New("usage: fleet retro trial --stop")
		}
		if err := retro.Stop(home); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "trial stopped; its runs stay in the ledger (fleet stats --runs --by arm)")
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: fleet retro trial [--share N] <patch-file> | fleet retro trial --stop")
	}
	b, err := os.ReadFile(pos[0])
	if err != nil {
		return fmt.Errorf("read patch: %w", err)
	}
	t, err := retro.Start(home, pos[0], string(b), *share, time.Now())
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "trial %s started: %d%% of new spawns get the variant; each arm needs %d closed runs for a verdict (fleet retro)\n",
		t.ID, t.Share, retro.MinArmRuns)
	return err
}

func retroPromote(out io.Writer, args []string) error {
	if len(args) != 0 {
		return errors.New("usage: fleet retro promote")
	}
	if err := humanOnly("promote"); err != nil {
		return err
	}
	home := logx.Home()
	t, ok, err := retro.Load(home)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("no trial is running")
	}
	rs, err := runs.Read(home, t.Started)
	if err != nil {
		return err
	}
	if err := retro.Promote(home, t, retro.Judge(t, rs)); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "trial %s promoted into %s/worker.md (the old one is worker.md.bak); trial ended\n", t.ID, home)
	return err
}
