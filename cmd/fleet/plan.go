package main

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/plan"
	"github.com/sturlabragason/fleet/internal/supervisor"
)

const runUsage = `usage: fleet run [--admiral NAME] <plan.toml> | fleet run --status [name]`

// cmdRun starts or resumes a plan file, or shows plan state (ADR-203).
func cmdRun(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	status := fs.Bool("status", false, "show a plan's tasks, or every plan's")
	owner := fs.String("admiral", "", "admiral that owns the plan's agents and gets their reports (default main)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *status {
		if fs.NArg() > 1 {
			return fmt.Errorf("%s", runUsage)
		}
		out, err := f.PlanStatus(fs.Arg(0))
		if err != nil {
			return err
		}
		fmt.Println(out)
		return nil
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("%s", runUsage)
	}
	if *owner != "" && *owner != supervisor.Main && !supervisor.Exists(f.Home, *owner) {
		return fmt.Errorf("no admiral %q (fleet admiral ls)", *owner)
	}
	path, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("plan path: %w", err)
	}
	p, err := plan.ParseFile(path)
	if err != nil {
		return err
	}
	if err := p.ResolveDirs(filepath.Dir(path)); err != nil {
		return err
	}
	st, err := f.RunPlan(p, *owner)
	if err != nil {
		return err
	}
	fmt.Println(st.Summary())
	return nil
}
