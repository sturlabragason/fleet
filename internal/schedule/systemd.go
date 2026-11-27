package schedule

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
)

// Fleet keeps no clock of its own: systemd parses the calendar and fires
// the timer, so a schedule runs while fleet is closed and catches up after
// sleep (Persistent=true). Linux only.
var (
	AnalyzeSpec = companion.Register(companion.Spec{Name: "systemd-analyze", Feature: "fleet schedule (calendar check)", Hint: "part of systemd; fleet schedule needs Linux with systemd --user"})
	CtlSpec     = companion.Register(companion.Spec{Name: "systemctl", Feature: "fleet schedule (timers)", Hint: "part of systemd; fleet schedule needs Linux with systemd --user"})
)

// Elapses are the next n times calendar spec fires after base (now when
// zero). An invalid spec is an error.
func Elapses(ctx context.Context, spec string, base time.Time, n int) ([]time.Time, error) {
	t, err := companion.Find(AnalyzeSpec)
	if err != nil {
		return nil, fmt.Errorf("calendar: %w", err)
	}
	args := []string{"calendar", "--iterations=" + strconv.Itoa(n)}
	if !base.IsZero() {
		args = append(args, "--base-time=@"+strconv.FormatInt(base.Unix(), 10))
	}
	out, err := t.Run(ctx, companion.Opts{}, append(args, spec)...)
	if err != nil {
		return nil, fmt.Errorf("calendar %q: %w", spec, err)
	}
	return parseElapses(string(out)), nil
}

// parseElapses reads the "(in UTC): Mon 2026-11-30 07:00:00 UTC" lines of
// systemd-analyze calendar.
func parseElapses(out string) []time.Time {
	var ts []time.Time
	for l := range strings.Lines(out) {
		_, v, ok := strings.Cut(l, "(in UTC): ")
		if !ok {
			continue
		}
		if t, err := time.Parse("Mon 2006-01-02 15:04:05 MST", strings.TrimSpace(v)); err == nil {
			ts = append(ts, t)
		}
	}
	return ts
}

// Times are where now sits in a schedule: the last due time at or before
// now (zero if none in the last two intervals), the next one and the gap
// between firings.
type Times struct {
	Prev, Next time.Time
	Interval   time.Duration
}

// When places now in spec's calendar.
func When(ctx context.Context, spec string, now time.Time) (Times, error) {
	e, err := Elapses(ctx, spec, now, 2)
	if err != nil {
		return Times{}, err
	}
	if len(e) < 2 {
		return Times{}, fmt.Errorf("calendar %q: fires fewer than twice", spec)
	}
	w := Times{Next: e[0], Interval: e[1].Sub(e[0])}
	// Look back far enough for uneven gaps: months, and weekday-only
	// calendars whose weekend gap is longer than the next one.
	back := 2 * w.Interval
	if w.Interval >= time.Hour && back < 8*24*time.Hour {
		back = 8 * 24 * time.Hour
	}
	p, err := Elapses(ctx, spec, now.Add(-back), int(back/w.Interval)+3)
	if err != nil {
		return w, err
	}
	for _, t := range p {
		if !t.After(now) {
			w.Prev = t
		}
	}
	return w, nil
}

// UnitDir is where systemd --user reads unit files.
func UnitDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "systemd", "user")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "systemd", "user")
}

// Unit is the base name of route's units; fleet-sched- keeps them apart
// from hand-made ones.
func Unit(route string) string { return "fleet-sched-" + route }

// serviceText is the oneshot the timer starts. KillMode=process keeps the
// tmux server a run may start (fleet up) alive after the unit exits.
// A login shell gives the run, and a fleet it starts, the user's PATH and
// keys rather than the unit's bare environment. route is a valid route name
// (no quotes or spaces); paths are quoted and their % escaped.
func serviceText(route, exe, fleetHome string) string {
	env := ""
	if fleetHome != "" {
		env = `Environment="FLEET_HOME=` + unitEsc(fleetHome) + "\"\n"
	}
	return fmt.Sprintf(`# Written by fleet schedule add (ADR-211); fleet schedule rm removes it.
[Unit]
Description=fleet schedule: route %[1]s

[Service]
Type=oneshot
%[3]sExecStart=/bin/bash -lc 'exec "$$0" schedule run --timer %[1]s' "%[2]s"
KillMode=process
TimeoutStartSec=5min
`, route, unitEsc(exe), env)
}

// unitEsc makes s safe inside a double-quoted unit-file value.
func unitEsc(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(s)
}

// timerText fires the service on spec and catches up once after downtime.
func timerText(route, spec string) string {
	return fmt.Sprintf(`# Written by fleet schedule add (ADR-211); fleet schedule rm removes it.
[Unit]
Description=fleet schedule: route %s

[Timer]
OnCalendar=%s
Persistent=true

[Install]
WantedBy=timers.target
`, route, spec)
}

func systemctl(ctx context.Context, args ...string) error {
	t, err := companion.Find(CtlSpec)
	if err != nil {
		return fmt.Errorf("timers: %w", err)
	}
	_, err = t.Run(ctx, companion.Opts{}, append([]string{"--user"}, args...)...)
	return err
}

// Install writes route's units and starts its timer.
func Install(ctx context.Context, route, spec, exe, fleetHome string) error {
	dir := UnitDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("unit dir: %w", err)
	}
	u := filepath.Join(dir, Unit(route))
	if err := os.WriteFile(u+".service", []byte(serviceText(route, exe, fleetHome)), 0o644); err != nil {
		return fmt.Errorf("write service: %w", err)
	}
	if err := os.WriteFile(u+".timer", []byte(timerText(route, spec)), 0o644); err != nil {
		return fmt.Errorf("write timer: %w", err)
	}
	if err := systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	// restart, not start: a changed calendar takes effect at once.
	if err := systemctl(ctx, "enable", Unit(route)+".timer"); err != nil {
		return err
	}
	return systemctl(ctx, "restart", Unit(route)+".timer")
}

// Uninstall stops route's timer and removes its units. Units already gone
// are not an error.
func Uninstall(ctx context.Context, route string) error {
	u := filepath.Join(UnitDir(), Unit(route))
	if _, err := os.Stat(u + ".timer"); err == nil {
		if err := systemctl(ctx, "disable", "--now", Unit(route)+".timer"); err != nil {
			return err
		}
	}
	for _, ext := range []string{".timer", ".service"} {
		if err := os.Remove(u + ext); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", Unit(route)+ext, err)
		}
	}
	return systemctl(ctx, "daemon-reload")
}

// TimerState is "active", "inactive" or "missing" (no unit file), with the
// unit file's mtime as when the schedule was set.
func TimerState(ctx context.Context, route string) (string, time.Time) {
	fi, err := os.Stat(filepath.Join(UnitDir(), Unit(route)+".timer"))
	if err != nil {
		return "missing", time.Time{}
	}
	if systemctl(ctx, "is-active", "--quiet", Unit(route)+".timer") != nil {
		return "inactive", fi.ModTime()
	}
	return "active", fi.ModTime()
}
