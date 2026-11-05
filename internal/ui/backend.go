// Package ui is the overview TUI: a grid of agent tiles sorted by attention.
package ui

import (
	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/team"
)

// Backend is what the TUI needs from the rest of fleet (fakeable in tests).
type Backend interface {
	Refresh() ([]*board.Agent, error)
	OnWall() map[string]bool
	Teams() map[string]team.Info        // agent -> team and colour
	Show(name string) (bool, error)     // into the ▶ slot or onto the wall; false = refused (wall full)
	Add(name string) (bool, error)      // as a new wall pane; false = refused (wall at 9)
	Next(dir int) (string, bool, error) // next (+1) / previous (-1) waiting agent into the ▶ slot; "" = none, false = refused (wall full)
	Target() string                     // agent in the ▶ target slot, "" if none (DESIGN.md §5e)
	Untarget() error                    // clear the ▶ target
	Preset(p string) error              // wall layout 1/2/4/6/9/m
	Hide(name string) error             // send back to its hidden window
	WallEnabled() bool
	ApplyWall() error // re-tile after a refresh (cheap; cached layout)
	// SupCtx is the admiral context meter: label ("34%") and colour,
	// both "" when the fill is unknown.
	SupCtx() (label, color string)
	SetSupCtx(label, color string) error // "" unsets the border option
	ResetSup() error                     // ask tmux to confirm, then reset the admiral
	Changes() error                      // open the what's new popup (DESIGN.md §5d)
	PickProfile() error                  // open the admiral profile picker (DESIGN.md §6.10)
}
