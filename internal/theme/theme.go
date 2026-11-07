// Package theme holds the colour palettes shared by the TUI and the tmux
// chrome, and the one that is active.
//
// A palette is a set of roles, not a set of shades: Working, Waiting, Select
// and the structural ladder (Base → Crust, Text → Overlay0) mean the same
// thing in every theme, only the hex changes. Catppuccin Mocha is the default;
// `theme = "..."` under [defaults] in config.toml picks another (ADR-55).
//
// The house brand's Lane blue is a print and web accent (1.8:1 on the Mocha
// base, unreadable here) and is deliberately not used. What carries over from
// the brand is the discipline: colour means status, never decoration, and at
// most one thing on screen is the accent. Before adding a colour or a status,
// read docs/style.md §4.
package theme

import (
	"fmt"
	"sort"
	"strings"
)

// Default is the palette used when config.toml names none.
const Default = "catppuccin"

// Swatch is a named colour.
type Swatch struct{ Name, Hex string }

// Palette is one theme: every role, filled. A role is a meaning, not a shade;
// give a new status its own glyph in internal/ui before giving it its own
// colour.
type Palette struct {
	Name  string // config.toml value, e.g. "neon-drive"
	Label string // human name, e.g. "Neon Drive"

	// Grounds, darkest last.
	Base, Mantle, Crust          string
	Surface0, Surface1, Surface2 string

	// Inks, dimmest last.
	Text, Subtext0, Overlay2, Overlay1, Overlay0 string

	// Status roles.
	Working string
	// WorkingDim is the dim step of the running-subagent wave: a darker
	// working colour, never grey, so it can't pass for a done dot
	// (DESIGN.md §4 Subagents).
	WorkingDim string
	Waiting    string
	WaitingAlt string // second pulse frame for waiting tiles
	Error      string
	Stuck      string
	Idle       string
	Gone       string

	// Select is the ▶ target slot and nothing else (ADR-37).
	Select     string
	Supervisor string

	// Team is the ordered team colour palette (DESIGN.md §5c). It leaves out
	// the status colours and the select colour.
	Team []Swatch
}

// Roles mirroring the active palette. They are variables, not constants, so
// Load can swap the theme; anything that caches a colour (a lipgloss style, a
// tmux format string) must either read them at render time or re-read them
// from an OnChange hook.
var (
	Base       = catppuccin.Base
	Mantle     = catppuccin.Mantle
	Crust      = catppuccin.Crust
	Surface0   = catppuccin.Surface0
	Surface1   = catppuccin.Surface1
	Surface2   = catppuccin.Surface2
	Text       = catppuccin.Text
	Subtext0   = catppuccin.Subtext0
	Overlay2   = catppuccin.Overlay2
	Overlay1   = catppuccin.Overlay1
	Overlay0   = catppuccin.Overlay0
	Working    = catppuccin.Working
	WorkingDim = catppuccin.WorkingDim
	Waiting    = catppuccin.Waiting
	WaitingAlt = catppuccin.WaitingAlt
	Error      = catppuccin.Error
	Stuck      = catppuccin.Stuck
	Idle       = catppuccin.Idle
	Gone       = catppuccin.Gone
	Select     = catppuccin.Select
	Supervisor = catppuccin.Supervisor
	Muted      = catppuccin.Overlay1

	// TeamPalette is the active palette's team colours.
	TeamPalette = catppuccin.Team
)

var active = catppuccin

var hooks []func()

// OnChange registers a function to run whenever the active palette changes,
// and runs it once now. A package that caches a derived value (a lipgloss
// style, a tmux format string) registers here from its init.
func OnChange(f func()) {
	hooks = append(hooks, f)
	f()
}

// Active returns the palette in use.
func Active() Palette { return active }

// Names lists the palette names, the default first and the rest sorted.
func Names() []string {
	out := []string{Default}
	for n := range palettes {
		if n != Default {
			out = append(out, n)
		}
	}
	sort.Strings(out[1:])
	return out
}

// Load makes the named palette active. "" means the default. An unknown name
// is an error: a typo must not silently leave the theme unchanged.
func Load(name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = Default
	}
	p, ok := palettes[name]
	if !ok {
		return fmt.Errorf("unknown theme %q: pick one of %s", name, strings.Join(Names(), ", "))
	}
	apply(p)
	return nil
}

func apply(p Palette) {
	active = p
	Base, Mantle, Crust = p.Base, p.Mantle, p.Crust
	Surface0, Surface1, Surface2 = p.Surface0, p.Surface1, p.Surface2
	Text, Subtext0 = p.Text, p.Subtext0
	Overlay2, Overlay1, Overlay0 = p.Overlay2, p.Overlay1, p.Overlay0
	Working, WorkingDim = p.Working, p.WorkingDim
	Waiting, WaitingAlt = p.Waiting, p.WaitingAlt
	Error, Stuck = p.Error, p.Stuck
	Idle, Gone = p.Idle, p.Gone
	Select, Supervisor = p.Select, p.Supervisor
	Muted = p.Overlay1
	TeamPalette = p.Team
	for _, f := range hooks {
		f()
	}
}

// Status returns the colour for an agent status; unknown statuses get Idle.
func (p Palette) Status(st string) string {
	switch st {
	case "working":
		return p.Working
	case "waiting":
		return p.Waiting
	case "error":
		return p.Error
	case "stuck":
		return p.Stuck
	case "gone":
		return p.Gone
	}
	return p.Idle
}

// Status returns the active palette's colour for an agent status.
func Status(st string) string { return active.Status(st) }

// TeamHex returns the active palette's hex for a team colour name, or "".
func TeamHex(name string) string {
	for _, sw := range TeamPalette {
		if strings.EqualFold(sw.Name, name) {
			return sw.Hex
		}
	}
	return ""
}

// AdmiralColor is the colour of an admiral's colour slot (DESIGN §6.12):
// slot 0 (main) is the supervisor colour, slots 1 and 2 are the last
// team-palette swatches but one and two, so no admiral shares a status
// colour or Select.
func AdmiralColor(slot int) string {
	n := len(TeamPalette)
	switch {
	case slot <= 0 || n < 6:
		return Supervisor
	case slot == 1:
		return TeamPalette[5].Hex
	}
	return TeamPalette[4].Hex
}
