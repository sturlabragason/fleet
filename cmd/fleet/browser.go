package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/browser"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/sysx"
)

// guardDetect finds a presenting signal; tests stub it.
var guardDetect browser.Detector = browser.Detect

// cmdBrowserGuard is what an agent's xdg-open, gio, $BROWSER and browser
// shims run (ADR-200): open in the default browser unless presenting or a
// never-open agent, else refuse and log the URL for handover.
func cmdBrowserGuard(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fleet browser-guard <opener> [args...] (run by the guard's shims)")
	}
	ctx := context.Background()
	name, rest := args[0], args[1:]
	a, home := guardAgent()
	if browser.Guarded(name, rest) {
		never, err := prefs.BrowserNeverOpen()
		if err != nil {
			logx.For("browser").Warn("never_open config unreadable; using the default list", "err", err)
		}
		if v := browser.Decide(ctx, home, a, never, guardDetect); !v.Open {
			url := browser.URL(name, rest)
			if err := browser.Record(home, browser.Blocked{Time: time.Now(), Agent: a.Name, URL: url, Reason: v.Reason}); err != nil {
				logx.For("browser").Error("blocked open not logged", "err", err)
			}
			logx.For("browser").Info("open blocked", slog.String("agent", a.Name), slog.String("reason", v.Reason))
			fmt.Fprintf(os.Stderr, "%s (%s)\n", browser.BlockedMsg, v.Reason)
			return exitCode(1)
		}
	}
	argv, err := browser.Real(name, rest, os.Getenv("PATH"), home)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = browser.RealEnv(os.Environ(), home)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return exitCode(ee.ExitCode())
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// guardAgent names the agent and its fleet home from its process tag
// (name@home, set at spawn) and reads its route and profile from the board.
func guardAgent() (browser.Agent, string) {
	name, home, _ := strings.Cut(os.Getenv(sysx.OwnerEnv), "@")
	if home == "" {
		home = logx.Home()
	}
	a := browser.Agent{Name: name}
	if b := board.NewStore(home).Load(); b != nil {
		if ag := b.Agents[name]; ag != nil {
			a.Profile = ag.Profile
			if ag.Card != nil {
				a.Route = ag.Card.Route
			}
		}
	}
	return a, home
}

// cmdPresent is `fleet present [on|off]`: the manual presenting switch, and
// with no argument the state plus the blocked opens to hand over.
func cmdPresent(args []string) error {
	home := logx.Home()
	switch {
	case len(args) == 1 && (args[0] == "on" || args[0] == "off"):
		if err := browser.SetPresent(home, args[0] == "on"); err != nil {
			return err
		}
	case len(args) != 0:
		return fmt.Errorf("usage: fleet present [on|off]")
	}
	out, err := browser.Status(context.Background(), home, guardDetect)
	fmt.Println(out)
	return err
}
