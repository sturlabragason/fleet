package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"

	"golang.org/x/term"

	"github.com/sturlabragason/fleet/internal/logo"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/prefs"
)

// cmdLogo plays the start-up banner on demand (ADR-171).
func cmdLogo(args []string) error {
	fs := flag.NewFlagSet("logo", flag.ContinueOnError)
	still := fs.Bool("no-anim", false, "draw the mark without moving")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse logo flags: %w", err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("usage: fleet logo [--no-anim]")
	}
	if !ttyOut() {
		fmt.Println(logo.Render(logo.Splash, logo.ASCII(os.Getenv)))
		return nil
	}
	return playBanner(context.Background(), logo.Animate(bannerGate(*still, true)))
}

// startBanner plays the banner while `fleet up` builds the session, so it
// costs no time unless the build is faster than the animation. wait blocks
// until it has finished or a key skipped it; stop ends it at once and gives
// the terminal back, for a return before the attach. Where it may not move,
// nothing is drawn: the attach clears the screen at once.
func startBanner(skip bool) (wait, stop func()) {
	if skip || !logo.Animate(bannerGate(false, false)) {
		return func() {}, func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := playBanner(ctx, true); err != nil {
			logx.For("up").Warn("banner", "err", err)
		}
	}()
	return func() { <-done; cancel() }, func() { cancel(); <-done }
}

// playBanner draws the banner at the terminal's size; a terminal too small
// for the splash gets the largest static mark that fits.
func playBanner(ctx context.Context, anim bool) error {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return fmt.Errorf("terminal size: %w", err)
	}
	return logo.Banner(ctx, os.Stdin, os.Stdout, w, h, logo.ASCII(os.Getenv), anim)
}

func bannerGate(still, explicit bool) logo.Gate {
	p, err := prefs.Load()
	if err != nil {
		p = nil // a bad config is reported by the command itself; animate by default
	}
	return logo.Gate{Getenv: os.Getenv, TTY: ttyOut() && term.IsTerminal(int(os.Stdin.Fd())), Off: still || !p.Animation(), Explicit: explicit}
}

func ttyOut() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

// cutFlag removes every name from args and reports whether it was there.
func cutFlag(args []string, name string) ([]string, bool) {
	out := slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == name })
	return out, len(out) != len(args)
}
