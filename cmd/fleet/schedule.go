package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/routes"
	"github.com/sturlabragason/fleet/internal/runs"
	"github.com/sturlabragason/fleet/internal/schedule"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/todo"
)

const scheduleUsage = `usage: fleet schedule ls [--json] | add <route> "<calendar>" | rm <route> | run [--timer] [--force] <route>`

// scheduleCmds are fleet schedule's subcommands (ADR-211).
var scheduleCmds = map[string]func(ctx context.Context, f *fleet.Fleet, args []string) error{
	"ls":  scheduleLs,
	"add": scheduleAdd,
	"rm":  scheduleRm,
	"run": scheduleRun,
}

func cmdSchedule(f *fleet.Fleet, args []string) error {
	if len(args) == 0 {
		args = []string{"ls"}
	}
	h := scheduleCmds[args[0]]
	if h == nil {
		return errors.New(scheduleUsage)
	}
	return h(context.Background(), f, args[1:])
}

func scheduleAdd(ctx context.Context, f *fleet.Fleet, args []string) error {
	if len(args) != 2 {
		return errors.New(`usage: fleet schedule add <route> "<calendar>"  (systemd OnCalendar, e.g. "Mon 08:00" or "*-*-01 09:00")`)
	}
	name, spec := args[0], strings.TrimSpace(args[1])
	if err := scheduleName(name); err != nil {
		return err
	}
	if _, err := routes.Get(f.Home, name); err != nil {
		return err
	}
	e, err := schedule.Elapses(ctx, spec, time.Time{}, 200)
	if err != nil {
		return err
	}
	if len(e) == 0 {
		return fmt.Errorf("calendar %q never fires", spec)
	}
	for _, t := range e {
		if schedule.Quiet(t.Local()) {
			return fmt.Errorf("calendar %q fires at %s, inside quiet hours (%02d:00-%02d:00), where a timer run is skipped", spec, t.Local().Format("Mon 15:04"), schedule.QuietFrom, schedule.QuietTo)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find the fleet binary: %w", err)
	}
	if err := routes.SetSchedule(f.Home, name, spec, time.Now()); err != nil {
		return err
	}
	if err := schedule.Install(ctx, name, spec, exe, os.Getenv("FLEET_HOME")); err != nil {
		return fmt.Errorf("route %s scheduled, but its timer is not running: %w", name, err)
	}
	fmt.Printf("route %s runs on %q; next run %s (timer %s.timer)\n", name, spec, e[0].Local().Format("Mon 02 Jan 15:04"), schedule.Unit(name))
	return nil
}

func scheduleRm(ctx context.Context, f *fleet.Fleet, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: fleet schedule rm <route>")
	}
	if err := scheduleName(args[0]); err != nil {
		return err
	}
	if err := schedule.Uninstall(ctx, args[0]); err != nil {
		return err
	}
	if err := routes.SetSchedule(f.Home, args[0], "", time.Now()); err != nil {
		return err
	}
	fmt.Printf("route %s is no longer scheduled; its timer is removed\n", args[0])
	return nil
}

func scheduleLs(ctx context.Context, f *fleet.Fleet, args []string) error {
	asJSON := len(args) == 1 && args[0] == "--json"
	if len(args) > 0 && !asJSON {
		return errors.New("usage: fleet schedule ls [--json]")
	}
	rs, err := routes.Load(f.Home)
	if err != nil {
		return err
	}
	es, err := schedule.Read(f.Home)
	if err != nil {
		return err
	}
	now := time.Now()
	ss := schedule.Statuses(ctx, rs, es, now)
	if asJSON {
		b, err := json.MarshalIndent(ss, "", "  ")
		if err != nil {
			return fmt.Errorf("encode schedules: %w", err)
		}
		fmt.Println(string(b))
		return nil
	}
	if len(ss) == 0 {
		fmt.Println(`no scheduled routes; add one with fleet schedule add <route> "<calendar>"`)
		return nil
	}
	ledger, _ := runs.Read(f.Home, now.AddDate(0, -3, 0))
	st := f.Store.Load()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ROUTE\tSCHEDULE\tNEXT\tLAST\tRESULT")
	for _, s := range ss {
		next := "-"
		if !s.Next.IsZero() {
			next = s.Next.Local().Format("Mon 02 Jan 15:04")
		}
		last, result := "-", "-"
		if s.Last != nil {
			last, result = s.Last.At.Local().Format("Mon 02 Jan 15:04"), runResult(*s.Last, st, ledger)
		}
		if p := s.Problem(now); p != "" {
			result += " · " + p
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", s.Route, s.Spec, next, last, result)
	}
	return w.Flush()
}

// runResult is how a firing went: its outcome, and for a started run the
// agent's state on the board or its last report in the run ledger.
func runResult(e schedule.Entry, st *board.Board, ledger []runs.Record) string {
	if e.Outcome != schedule.Started {
		return e.Outcome + " (" + e.Reason + ")"
	}
	if _, ok := st.Agents[e.Agent]; ok {
		return e.Agent + " running"
	}
	for _, r := range slices.Backward(ledger) {
		if r.Name == e.Agent && !r.Created.Before(e.At.Add(-time.Minute)) {
			if r.Last == "" {
				return e.Agent + " closed without a report"
			}
			return e.Agent + " " + r.Last
		}
	}
	return e.Agent + " closed"
}

func scheduleRun(ctx context.Context, f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("schedule run", flag.ContinueOnError)
	timer := fs.Bool("timer", false, "fired by the route's timer: check quiet hours, catch-up window, cap and quota")
	force := fs.Bool("force", false, "skip every check, overlap included")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		return errors.New("usage: fleet schedule run [--timer] [--force] <route>")
	}
	r, err := routes.Get(f.Home, fs.Arg(0))
	if err != nil {
		return err
	}
	now := time.Now()
	e := schedule.Entry{Route: r.Name, At: now, Manual: !*timer}
	g := schedule.Gate{Now: now, Timer: *timer}
	if *timer {
		if r.Schedule == "" {
			return fmt.Errorf("route %s has no schedule; remove its timer with fleet schedule rm %s", r.Name, r.Name)
		}
		w, err := schedule.When(ctx, r.Schedule, now)
		if err != nil {
			return err
		}
		e.Due, g.Due, g.Interval = w.Prev, w.Prev, w.Interval
	}
	es, err := schedule.Read(f.Home)
	if err != nil {
		return err
	}
	st := f.Store.Load()
	onBoard := func(n string) bool { _, ok := st.Agents[n]; return ok }
	if l, ok := schedule.LastStarted(es, r.Name); ok && onBoard(l.Agent) {
		g.LastOpen = l.Agent
	}
	g.Running = schedule.OpenAgents(es, onBoard)
	if *timer {
		g.QuotaLow = schedule.QuotaLow(f.Home, r.Profile, f.Prefs.Burn().ReservePct, now)
	}
	if why := g.Check(); why != "" && !*force {
		e.Outcome, e.Reason = schedule.Skipped, why
		return scheduleEnd(f, e)
	}
	e.Agent = scheduleAgentName(r.Name, now, onBoard)
	if err := scheduleSpawn(ctx, f, r, e); err != nil {
		e.Outcome, e.Reason, e.Agent = schedule.Failed, err.Error(), ""
		return errors.Join(scheduleEnd(f, e), err)
	}
	e.Outcome = schedule.Started
	return scheduleEnd(f, e)
}

// scheduleSpawn starts the run's agent through its route, as fleet spawn
// --route does, after bringing the fleet up: a queued spawn only starts from
// the overview, and reports need the admiral.
func scheduleSpawn(ctx context.Context, f *fleet.Fleet, r routes.Route, e schedule.Entry) error {
	if !f.T.HasSession(fleet.Session) {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("find the fleet binary: %w", err)
		}
		if out, err := exec.CommandContext(ctx, exe, "up", "--no-attach").CombinedOutput(); err != nil {
			return fmt.Errorf("fleet up: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	due := "now, by hand"
	if !e.Due.IsZero() {
		due = e.Due.Local().Format("Mon 02 Jan 15:04")
	}
	task := fmt.Sprintf("Scheduled run of route %s (due %s, fleet schedule, ADR-211). Nobody is watching: work unattended, then report done, or blocked/question if you need a decision.", r.Name, due)
	o := fleet.SpawnOpts{Card: &board.Card{Goal: "scheduled " + r.Name + " run (" + due + ")"}}
	name, profile, dir, prompt, err := spawnRoute(f, r.Name, []string{e.Agent, task}, &o, nil)
	if err != nil {
		return err
	}
	_, err = f.SpawnWith(o, name, profile, dir, prompt)
	return err
}

// scheduleName refuses a route name that is unsafe in a unit file or too
// long for its agents' names (32 characters with -MMDD-HHMM added).
func scheduleName(route string) error {
	if !routes.ValidName(route) || len(route) > 22 {
		return fmt.Errorf("route name %q: a scheduled route needs lowercase letters, digits, - and _, at most 22 characters", route)
	}
	return nil
}

// scheduleAgentName is <route>-MMDD, with the time added when that name is
// taken.
func scheduleAgentName(route string, now time.Time, taken func(string) bool) string {
	n := route + "-" + now.Format("0102")
	if taken(n) {
		n += "-" + now.Format("1504")
	}
	return n
}

// scheduleEnd logs the firing and, when it did not start, files an open item
// for the admiral so a missed run is seen (ADR-211).
func scheduleEnd(f *fleet.Fleet, e schedule.Entry) error {
	logErr := schedule.Append(f.Home, e)
	if e.Outcome == schedule.Started {
		fmt.Printf("route %s: started %s\n", e.Route, e.Agent)
		return logErr
	}
	text := fmt.Sprintf("Scheduled run of route %s %s: %s. Start it by hand with `fleet schedule run %s` if it is still needed.", e.Route, e.Outcome, e.Reason, e.Route)
	if e.Manual { // the user is at the terminal: tell them, not the admiral
		return errors.Join(logErr, fmt.Errorf("route %s %s: %s (--force skips the checks)", e.Route, e.Outcome, e.Reason))
	}
	fmt.Println(text)
	if err := supervisor.AppendNoteBy(f.Home, supervisor.Main, "", text); err != nil {
		return errors.Join(logErr, err)
	}
	_, _, err := f.AddItem(supervisor.Main, todo.Request, text, "", "")
	return errors.Join(logErr, err)
}
