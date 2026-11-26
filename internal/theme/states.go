package theme

// State is how one agent status draws, everywhere fleet draws it: the TUI
// tiles, the tmux pane borders and the web board (ADR-201). Role names the
// palette colour; Rank sorts by attention, lower first.
type State struct {
	Role  string
	Glyph string
	Rank  int
}

// states is the one state table. Glyphs are static (ADR-81): colour tells
// working ○ from idle ○, never motion. Statuses with no entry draw as
// unknownState.
var states = map[string]State{
	"waiting":  {"waiting", "●", 0},
	"error":    {"error", "✖", 1},
	"stuck":    {"stuck", "⧗", 2},
	"working":  {"working", "○", 3},
	"starting": {"working", "▸", 3},
	"idle":     {"idle", "○", 4},
	"unknown":  {"idle", "?", 5},
	"offline":  {"idle", "⊘", 6},
	"gone":     {"gone", "·", 6},
	"queued":   {"gone", "‖", 7},
	"parked":   {"gone", "z", 8},
}

var unknownState = State{"idle", "?", 6}

// reports is the same table for a report's state on a tile.
var reports = map[string]State{
	"done":     {Role: "working", Glyph: "✓"},
	"blocked":  {Role: "error", Glyph: "✗"},
	"question": {Role: "waiting", Glyph: "?"},
}

var unknownReport = State{Role: "idle", Glyph: "…"}

// StateOf returns how an agent status draws.
func StateOf(st string) State {
	if s, ok := states[st]; ok {
		return s
	}
	return unknownState
}

// States lists every status the table knows, for drawing all of them (the
// web board's CSS).
func States() map[string]State { return states }

// Glyph is the mark for an agent status.
func Glyph(st string) string { return StateOf(st).Glyph }

// Rank is the attention rank of an agent status: lower sorts first.
func Rank(st string) int { return StateOf(st).Rank }

// Report returns the glyph and colour of a report state.
func Report(state string) (glyph, hex string) {
	s, ok := reports[state]
	if !ok {
		s = unknownReport
	}
	return s.Glyph, active.Role(s.Role)
}

// Role returns the palette colour for a status role, Idle for an unknown one.
func (p Palette) Role(role string) string {
	switch role {
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
