package main

import (
	"context"

	"github.com/sturlabragason/fleet/internal/fleet"
)

// extension is what a feature compiled in with a build tag adds to the CLI
// (DESIGN §15 "Extensions", ADR-126). It calls register from its own init();
// a default build registers nothing: no extra commands, nothing runs, the
// usage text is the one above.
type extension struct {
	Name string
	// Commands are extra top-level commands that need no fleet state.
	Commands map[string]func(args []string) error
	// Usage edits the usage text, e.g. adds its commands' lines.
	Usage func(text string) string
	// Before may refuse a command that needs fleet state, before it runs.
	Before func(home, cmd string) error
	// Open runs when the overview, the watcher or the admiral's MCP server starts.
	Open func(f *fleet.Fleet)
	// MCP may refuse to start an MCP server for this admiral and agent.
	MCP func(f *fleet.Fleet, admiral, agent string) error
	// UI runs in the background while the overview is up.
	UI func(ctx context.Context, f *fleet.Fleet)
	// Tmux are extra tmux commands (a bind-key, an option) run after
	// fleet's own when it configures its tmux server.
	Tmux func(f *fleet.Fleet) [][]string
}

var (
	exts    []extension
	extCmds = map[string]func(args []string) error{}
)

// register adds an extension; every registered extension's hooks run, in
// registration order. A name or command registered twice panics.
func register(e extension) {
	if e.Name == "" {
		panic("register: extension has no name")
	}
	for _, x := range exts {
		if x.Name == e.Name {
			panic("register: extension " + e.Name + " registered twice")
		}
	}
	for c := range e.Commands {
		if extCmds[c] != nil {
			panic("register: command " + c + " registered twice")
		}
	}
	exts = append(exts, e)
	for c, h := range e.Commands {
		extCmds[c] = h
	}
	if e.Usage != nil {
		usage = e.Usage(usage)
	}
}

// extBefore runs every extension's check; the first refusal wins.
func extBefore(home, cmd string) error {
	for _, e := range exts {
		if e.Before != nil {
			if err := e.Before(home, cmd); err != nil {
				return err
			}
		}
	}
	return nil
}

func runExtOpen(f *fleet.Fleet) {
	for _, e := range exts {
		if e.Open != nil {
			e.Open(f)
		}
	}
}

// extMCP runs every extension's MCP check; the first refusal wins.
func extMCP(f *fleet.Fleet, admiral, agent string) error {
	for _, e := range exts {
		if e.MCP != nil {
			if err := e.MCP(f, admiral, agent); err != nil {
				return err
			}
		}
	}
	return nil
}

// runExtUI starts every extension's background overview work.
func runExtUI(ctx context.Context, f *fleet.Fleet) {
	for _, e := range exts {
		if e.UI != nil {
			go e.UI(ctx, f)
		}
	}
}

// extTmux is every extension's tmux commands, in registration order.
func extTmux(f *fleet.Fleet) [][]string {
	var out [][]string
	for _, e := range exts {
		if e.Tmux != nil {
			out = append(out, e.Tmux(f)...)
		}
	}
	return out
}
