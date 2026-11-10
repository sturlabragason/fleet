package main

import (
	"fmt"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/wall"
)

// cmdTakeover is `fleet takeover <name> [--acp]` (ADR-82): the agent's
// session moves to the tool's own screen (or back to the acp host with
// --acp), into the wall slot it had.
func cmdTakeover(f *fleet.Fleet, args []string) error {
	toACP, name := false, ""
	for _, a := range args {
		switch {
		case a == "--acp":
			toACP = true
		case name == "" && a != "" && a[0] != '-':
			name = a
		default:
			return fmt.Errorf("usage: fleet takeover <name> [--acp]")
		}
	}
	if name == "" {
		return fmt.Errorf("usage: fleet takeover <name> [--acp]")
	}
	w := wall.New(f.T, fleet.Session)
	shown := w.OnWall()[name]
	a, err := f.Takeover(name, toACP)
	if err != nil {
		return err
	}
	if shown && a.PaneID != "" {
		if _, err := w.Place(a.PaneID); err != nil {
			fmt.Printf("note: %s is not back on the wall: %v\n", name, err)
		}
	}
	driver := "the tool's own screen (tmux)"
	if toACP {
		driver = "fleet's acp host"
	}
	fmt.Printf("%s: session %s now runs in %s, pane %s\n", name, a.SessionID, driver, a.PaneID)
	return nil
}
