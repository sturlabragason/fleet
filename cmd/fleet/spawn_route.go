package main

import (
	"fmt"
	"strings"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/routes"
)

// spawnRoute is fleet spawn --route NAME: args are <name> ["task"], and the
// route fills what the flags left unset, the same way fleet_spawn route=NAME
// does. A systemd timer uses it to start a recurring pass through its route.
func spawnRoute(f *fleet.Fleet, route string, args []string, o *fleet.SpawnOpts, interactive *bool) (name, profile, dir, prompt string, err error) {
	if len(args) < 1 {
		return "", "", "", "", fmt.Errorf("usage: fleet spawn --route <route> <name> [\"task\"]")
	}
	r, err := routes.Get(f.Home, route)
	if err != nil {
		return "", "", "", "", fmt.Errorf("route %s: %w", route, err)
	}
	c := board.Card{}
	if o.Card != nil {
		c = *o.Card
	}
	sp := routes.Spawn{Effort: o.Effort, Model: o.Model, Check: c.Check, Worktree: o.Worktree,
		Interactive: interactive, Prompt: strings.Join(args[1:], " ")}
	r.Fill(&sp)
	if sp.Profile == "" || sp.Dir == "" {
		return "", "", "", "", fmt.Errorf("route %s has no profile or dir", route)
	}
	o.Effort, o.Model, o.Worktree = sp.Effort, sp.Model, sp.Worktree
	if sp.Interactive != nil {
		o.Role.Interactive = *sp.Interactive
	}
	c.Check, c.May, c.Route = sp.Check, sp.May, r.Name
	o.Card = fleet.NormalizeCard(&c)
	return args[0], sp.Profile, sp.Dir, sp.Prompt, nil
}
