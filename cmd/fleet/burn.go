package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/burn"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/runs"
)

func cmdBurn(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("burn", flag.ContinueOnError)
	tick := fs.Bool("tick", false, "timer tick mode: evaluate burnable profiles and append to logs/burn.jsonl")
	live := fs.Bool("live", false, "override dry_run = true and actually spawn/wrap-up agents for this run")
	dryRun := fs.Bool("dry-run", false, "override dry_run = false and log the plan without spawning or messaging")
	asJSON := fs.Bool("json", false, "output JSON")
	profileFlag := fs.String("profile", "", "check only this profile")
	review := fs.Bool("review", false, "generate morning Accept/Reject HTML review page for fleet/burn-* branches")
	outPath := fs.String("out", "", "output path for --review HTML (default: <fleet home>/burn-review.html; - for stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	now := time.Now()

	if *review {
		return runBurnReview(ctx, f.Home, now, *outPath, os.Stdout)
	}

	cfg := f.Prefs.Burn()
	if *live {
		cfg.DryRun = false
	}
	if *dryRun {
		cfg.DryRun = true
	}

	profiles := cfg.Profiles
	if p := strings.TrimSpace(*profileFlag); p != "" {
		// --profile narrows the allowlist, never widens it: an owner's opt-in is the gate.
		if !slices.Contains(cfg.Profiles, p) {
			return fmt.Errorf("profile %q is not in [burn] profiles; add it there to allow burning it", p)
		}
		profiles = []string{p}
	}
	if len(profiles) == 0 {
		if *asJSON {
			return writeBurnJSON(os.Stdout, burn.TickResult{At: now, DryRun: cfg.DryRun})
		}
		fmt.Println("fleet burn: no profiles configured in [burn] profiles (config.toml); nothing to do.")
		return nil
	}

	quotas := make([]burn.Quota, 0, len(profiles))
	for _, p := range profiles {
		pcfg := cfg.ForProfile(p)
		prof := f.Profiles[p]
		rname := pcfg.Reader
		if rname == "" {
			rname = burn.InferReader(p, prof.Command)
		}
		r := burn.NewReader(rname)
		if r == nil {
			quotas = append(quotas, burn.Quota{
				Profile:   p,
				Reader:    rname,
				Available: false,
				Error:     fmt.Sprintf("unknown or unset quota reader %q (set [burn.%s] reader)", rname, p),
				FetchedAt: now,
			})
			continue
		}
		quotas = append(quotas, r.Read(ctx, p, pcfg, expandProfileEnv(prof.Env)))
	}

	history, _ := runs.Read(f.Home, now.AddDate(0, 0, -30))
	tasks, taskErr := burn.ReadTasks(ctx, cfg.Tag)
	taskErrStr := ""
	if taskErr != nil {
		taskErrStr = taskErr.Error()
	}

	cwd, _ := os.Getwd()
	tasks = burn.DropPendingReview(ctx, tasks, cwd)

	st := f.Store.Load()
	agents := make([]*board.Agent, 0, len(st.Agents))
	for _, a := range st.Agents {
		agents = append(agents, a)
	}

	res := burn.Plan(burn.PlanInput{
		Now:        now,
		Config:     cfg,
		MaxWorking: f.Prefs.MaxWorking(),
		Quotas:     quotas,
		Runs:       history,
		Tasks:      tasks,
		TaskError:  taskErrStr,
		Active:     burn.ActiveFromBoard(agents),
		DefaultDir: cwd,
	})

	if !res.DryRun {
		executeBurnActions(f, &res)
	}

	if *tick {
		if err := burn.AppendLog(f.Home, res); err != nil {
			fmt.Fprintf(os.Stderr, "fleet burn: warning: %v\n", err)
		}
	}

	if *asJSON {
		return writeBurnJSON(os.Stdout, res)
	}
	writeBurnText(os.Stdout, res)
	return nil
}

func executeBurnActions(f *fleet.Fleet, res *burn.TickResult) {
	yes := true
	for i := range res.Profiles {
		pp := &res.Profiles[i]
		for j := range pp.WrapUps {
			w := &pp.WrapUps[j]
			if err := f.Send(w.Agent, burn.WrapUpMessage(w.Profile, w.TimeToReset)); err != nil {
				w.Error = err.Error()
			} else {
				w.Sent = true
			}
		}
		for j := range pp.PlannedSpawns {
			sp := &pp.PlannedSpawns[j]
			opts := fleet.SpawnOpts{
				Worktree: &yes,
				Effort:   sp.Effort,
				Card: &board.Card{
					Goal:   sp.Task.Description,
					Result: "result.md",
					May:    []string{fleet.ReviewOnly},
				},
			}
			if _, err := f.SpawnWith(opts, sp.Agent, sp.Profile, sp.Dir, sp.Prompt); err != nil {
				sp.Error = err.Error()
			} else {
				sp.Spawned = true
			}
		}
	}
}

func runBurnReview(ctx context.Context, home string, now time.Time, outPath string, stdout io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	branches, err := burn.CollectBurnBranches(ctx, cwd)
	if err != nil {
		return err
	}
	html, err := burn.RenderReviewHTML(now, branches)
	if err != nil {
		return err
	}
	if outPath == "-" {
		_, err = io.WriteString(stdout, html)
		return err
	}
	if outPath == "" {
		outPath = filepath.Join(home, "burn-review.html")
	}
	if err := os.WriteFile(outPath, []byte(html), 0o600); err != nil {
		return fmt.Errorf("write review page: %w", err)
	}
	fmt.Fprintf(stdout, "wrote morning review page (%d branch(es)): %s\n", len(branches), outPath)
	return nil
}

func expandProfileEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = os.ExpandEnv(v)
	}
	return out
}

func writeBurnJSON(w io.Writer, res burn.TickResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

func writeBurnText(out io.Writer, res burn.TickResult) {
	mode := "DRY-RUN"
	if !res.DryRun {
		mode = "LIVE"
	}
	fmt.Fprintf(out, "fleet burn [%s] %s · +burn tasks: %d\n", mode, res.At.Format("2006-01-02 15:04 MST"), res.TasksFound)
	if res.TaskError != "" {
		fmt.Fprintf(out, "  tasks: %s\n", res.TaskError)
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "PROFILE\tREADER\tLEFT\tRESET IN\tN\tACTION")
	for _, pp := range res.Profiles {
		left := "-"
		resetIn := "-"
		if pp.Window != nil {
			left = fmt.Sprintf("%.1f%%", pp.Window.RemainingPct)
			if pp.Window.LimitTokens > 0 {
				left = fmt.Sprintf("%.1f%% (%dM/%dM)", pp.Window.RemainingPct, pp.Window.RemainingTokens/1_000_000, pp.Window.LimitTokens/1_000_000)
			}
			if pp.TimeToReset > 0 {
				resetIn = pp.TimeToReset.Round(time.Minute).String()
			}
		}
		action := pp.SkipReason
		if len(pp.WrapUps) > 0 {
			action = fmt.Sprintf("wrap-up %d agent(s)", len(pp.WrapUps))
		} else if len(pp.PlannedSpawns) > 0 {
			verb := "would spawn"
			if !res.DryRun {
				verb = "spawned"
			}
			action = fmt.Sprintf("%s %d agent(s)", verb, len(pp.PlannedSpawns))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d/%d\t%s\n", pp.Profile, pp.Reader, left, resetIn, pp.CappedN, pp.ComputedN, action)
	}
	_ = tw.Flush()
	for _, pp := range res.Profiles {
		for _, sp := range pp.PlannedSpawns {
			verb := "would spawn"
			if sp.Spawned {
				verb = "spawned"
			}
			line := fmt.Sprintf("  %s %s (%s, effort=%s): %s", verb, sp.Agent, sp.Profile, sp.Effort, sp.Task.Description)
			if sp.Error != "" {
				line += " [error: " + sp.Error + "]"
			}
			fmt.Fprintln(out, line)
		}
		for _, wu := range pp.WrapUps {
			verb := "would wrap-up"
			if wu.Sent {
				verb = "wrapped-up"
			}
			line := fmt.Sprintf("  %s %s (%s, reset in %s)", verb, wu.Agent, wu.Profile, wu.TimeToReset.Round(time.Minute))
			if wu.Error != "" {
				line += " [error: " + wu.Error + "]"
			}
			fmt.Fprintln(out, line)
		}
	}
}
