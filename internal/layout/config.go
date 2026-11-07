// Package layout owns ALL pane geometry of the main fleet window: admiral
// side and width, overview strip height and the wall presets. Change the
// numbers here; nothing else hard-codes sizes.
package layout

// Config holds every geometry knob.
type Config struct {
	SupSide       string // "left" or "right"
	SupWidth      int    // columns when window >= NarrowBelow
	SupWidthNarr  int    // columns when window < NarrowBelow
	NarrowBelow   int    // window width threshold
	SupHidden     bool
	SupMinRows    int // an admiral pane is never shorter than this when several stack
	OverviewRows  int // full overview: 2 tile rows + header + chips
	ChipsOnlyRows int // compact overview
	ChipsOnly     bool
	MainPercent   int // M preset: main pane share of wall width
	MinPaneW      int // comfortable wall pane width the grid scores against
	MinPaneH      int // comfortable wall pane height (incl. the border-status row)
	FloorDiv      int // hard floor: 1/FloorDiv of the wall rectangle (see floor.go)
	FloorMinW     int // hard floor: absolute lower bound on pane width
	FloorMinH     int // hard floor: absolute lower bound on pane height
	FocusPercent  int // emphasis pane share of the wall
	TileW, TileH  int // overview tile: minimum width and height (used by the TUI)
	TileComfortW  int // overview tiles wrap onto more rows to reach this width
	TileMaxW      int // overview tiles never grow wider than this
}

// Default is the designer-reviewed layout.
var Default = Config{
	SupSide:       "left",
	SupWidth:      90,
	SupWidthNarr:  64,
	NarrowBelow:   220,
	SupMinRows:    8,
	OverviewRows:  14,
	ChipsOnlyRows: 3,
	MainPercent:   60,
	MinPaneW:      64,
	MinPaneH:      17,
	FloorDiv:      4,
	FloorMinW:     40,
	FloorMinH:     10,
	FocusPercent:  60,
	TileW:         26,
	TileH:         6,
	TileComfortW:  34,
	TileMaxW:      48,
}

// SupCols returns the admiral width for a window width.
func (c Config) SupCols(w int) int {
	if w < c.NarrowBelow {
		return c.SupWidthNarr
	}
	return c.SupWidth
}

// OvRows returns the overview height.
func (c Config) OvRows() int {
	if c.ChipsOnly {
		return c.ChipsOnlyRows
	}
	return c.OverviewRows
}

// Presets are the wall layouts: name -> slots.
var Presets = map[string]int{"1": 1, "2": 2, "4": 4, "6": 6, "9": 9, "m": 4}

// Slots returns the slot cap for a preset. Presets cap how many panes may be
// on the wall; the grid (see grid.go) decides the shape.
func (c Config) Slots(preset string) int {
	n, ok := Presets[preset]
	if !ok {
		n = 4
	}
	return n
}
