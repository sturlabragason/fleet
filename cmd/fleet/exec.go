package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/rexec"
)

// cmdExec is `fleet exec [--host H] -- <cmd...>`: run a build or test for
// this worktree on the [exec] host, or here when none is set (ADR-107).
func cmdExec(args []string) error {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	host := fs.String("host", "", "ssh destination; default [exec] host in config.toml")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cmd := fs.Args()
	if len(cmd) == 0 {
		return fmt.Errorf("usage: fleet exec [--host H] -- <command...>")
	}
	cfg := prefs.Exec{}
	if p, err := prefs.Load(); err == nil {
		cfg = p.Exec()
	} else {
		return err
	}
	if *host != "" {
		cfg.Host = *host
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	if cfg.Host == "" {
		return runExec(exec.Command("sh", "-c", rexec.Line(cmd)))
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return fmt.Errorf("fleet exec: not in a git worktree: %w", err)
	}
	plan := rexec.Make(cfg.Host, cfg.Root, strings.TrimSpace(string(out)), dir, cmd)
	sync := exec.Command(plan.Sync[0], plan.Sync[1:]...)
	sync.Stdout, sync.Stderr = os.Stderr, os.Stderr
	if err := sync.Run(); err != nil {
		return fmt.Errorf("fleet exec: copying the worktree to %s: %w", cfg.Host, err)
	}
	return runExec(exec.Command(plan.Run[0], plan.Run[1:]...))
}

// runExec runs c with this process's stdio and passes its exit code on.
func runExec(c *exec.Cmd) error {
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := c.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return exitCode(ee.ExitCode())
	}
	return err
}
