package main

import (
	"context"

	"github.com/sturlabragason/fleet/internal/fleet"
)

// Hooks for optional features compiled in with a build tag (DESIGN §15,
// "Extensions"). A default build leaves them all empty: no extra commands,
// nothing runs, the usage text is the one above.
var (
	// extCmds are extra top-level commands that need no fleet state.
	extCmds = map[string]func(args []string) error{}
	// extBefore may refuse a command that needs fleet state, before it runs.
	extBefore func(home, cmd string) error
	// extOpen runs when the overview, the watcher or the admiral's MCP server starts.
	extOpen func(f *fleet.Fleet)
	// extMCP may refuse to start an MCP server for this admiral and agent.
	extMCP func(f *fleet.Fleet, admiral, agent string) error
	// extUI runs in the background while the overview is up.
	extUI func(ctx context.Context, f *fleet.Fleet)
)

func runExtOpen(f *fleet.Fleet) {
	if extOpen != nil {
		extOpen(f)
	}
}
