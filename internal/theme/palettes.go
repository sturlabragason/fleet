package theme

// The named palettes. Catppuccin Mocha is the default and the only one that
// was ever shipped; the other four come from the theme drafts, with their
// contrast numbers checked in palettes_test.go (ADR-55).

// Catppuccin Mocha (https://catppuccin.com/palette): the default.
var catppuccin = Palette{
	Name: "catppuccin", Label: "Catppuccin Mocha",
	Base: "#1e1e2e", Mantle: "#181825", Crust: "#11111b",
	Surface0: "#313244", Surface1: "#45475a", Surface2: "#585b70",
	Text: "#cdd6f4", Subtext0: "#a6adc8", Overlay2: "#9399b2", Overlay1: "#7f849c", Overlay0: "#6c7086",
	Working: "#a6e3a1", WorkingDim: "#628067",
	Waiting: "#f9e2af", WaitingAlt: "#fab387",
	Error: "#f38ba8", Stuck: "#eba0ac",
	Idle: "#7f849c", Gone: "#585b70",
	Select: "#b4befe", Supervisor: "#cba6f7",
	Team: []Swatch{
		{"blue", "#89b4fa"}, {"pink", "#f5c2e7"}, {"teal", "#94e2d5"},
		{"mauve", "#cba6f7"}, {"sky", "#89dceb"}, {"sapphire", "#74c7ec"},
	},
}

// Stealth: near-black, high contrast, one ember for the ▶ target.
var stealth = Palette{
	Name: "stealth", Label: "Stealth",
	Base: "#0a0b0d", Mantle: "#07080a", Crust: "#000000",
	Surface0: "#16181c", Surface1: "#23262c", Surface2: "#3a3f47",
	Text: "#e7ebf0", Subtext0: "#aeb6c0", Overlay2: "#8b939e", Overlay1: "#636b76", Overlay0: "#4a515a",
	Working: "#53d6a0", WorkingDim: "#2d7a5d",
	Waiting: "#ffcf4d", WaitingAlt: "#ffa733",
	Error: "#ff4d5e", Stuck: "#ff8f5c",
	Idle: "#636b76", Gone: "#3a3f47",
	Select: "#ff7a18", Supervisor: "#7aa2ff",
	Team: []Swatch{
		{"blue", "#7aa2ff"}, {"pink", "#e07ad6"}, {"teal", "#48cfc0"},
		{"mauve", "#a88bff"}, {"sky", "#64c8f0"}, {"sapphire", "#4a8fd8"},
	},
}

// Neon Drive: cyberpunk magenta/cyan on deep navy.
var neonDrive = Palette{
	Name: "neon-drive", Label: "Neon Drive",
	Base: "#0b0f2a", Mantle: "#080b1f", Crust: "#040616",
	Surface0: "#141a3d", Surface1: "#1e2752", Surface2: "#33407a",
	Text: "#e4ecff", Subtext0: "#b6c4ee", Overlay2: "#8d9bd0", Overlay1: "#6473a8", Overlay0: "#4b5889",
	Working: "#00e5ff", WorkingDim: "#0b7f93",
	// The waiting pulse goes amber↔pink here: peach would sit next to the
	// stuck hue, and the pair has to read as one blinking thing.
	Waiting: "#ffd166", WaitingAlt: "#ffa0e8",
	Error: "#ff3864", Stuck: "#ff7ad9",
	Idle: "#6473a8", Gone: "#33407a",
	Select: "#ff2bd1", Supervisor: "#a06bff",
	Team: []Swatch{
		{"blue", "#4d8cff"}, {"pink", "#ff8ad8"}, {"teal", "#64ffda"},
		{"mauve", "#a06bff"}, {"sky", "#7ee0ff"}, {"sapphire", "#3f6fd8"},
	},
}

// Phosphor: P1 green CRT, amber attention, one red alarm. Teams ride the
// green→teal ramp, told apart by lightness (ADR-55).
var phosphor = Palette{
	Name: "phosphor", Label: "Phosphor",
	Base: "#06110a", Mantle: "#040c07", Crust: "#020704",
	Surface0: "#0c1d13", Surface1: "#143021", Surface2: "#265139",
	Text: "#c8ffd9", Subtext0: "#8fe7ab", Overlay2: "#6bc78a", Overlay1: "#49996a", Overlay0: "#35724f",
	Working: "#33ff77", WorkingDim: "#1a8a44",
	Waiting: "#ffc044", WaitingAlt: "#ffe08a",
	Error: "#ff3b2f", Stuck: "#ff7a45",
	Idle: "#49996a", Gone: "#265139",
	// Pure white, not a lime: on a green screen a brighter green reads as a
	// louder "working", which is exactly what the ▶ target must not say.
	Select: "#ffffff", Supervisor: "#7cf0c8",
	Team: []Swatch{
		{"lime", "#b6ff6a"}, {"mint", "#7cf0c8"}, {"teal", "#3fd4c0"},
		{"moss", "#6fae72"}, {"sage", "#a9e3b4"}, {"fern", "#2f9d5b"},
	},
}

// Graphite: greys only; white = waiting, red = error, amber = ▶. Teams are
// six steps of one grey ramp plus the team name (ADR-55).
var graphite = Palette{
	Name: "graphite", Label: "Graphite",
	Base: "#0d0d0f", Mantle: "#0a0a0c", Crust: "#060607",
	Surface0: "#17171a", Surface1: "#232327", Surface2: "#3b3b41",
	Text: "#f2f2f4", Subtext0: "#c3c3c8", Overlay2: "#9a9aa2", Overlay1: "#6e6e76", Overlay0: "#54545b",
	Working: "#a8a8b0", WorkingDim: "#5e5e66",
	// Waiting cannot lean on hue here: the pulse is white↔grey and the ● glyph
	// is the real signal.
	Waiting: "#ffffff", WaitingAlt: "#b9b9c0",
	Error: "#ff2200", Stuck: "#ff7b5e",
	Idle: "#6e6e76", Gone: "#3b3b41",
	Select: "#ffa000", Supervisor: "#f2f2f4",
	Team: []Swatch{
		{"chalk", "#e6e6ea"}, {"silver", "#c3c3c8"}, {"steel", "#9a9aa2"},
		{"slate", "#7f7f88"}, {"ash", "#6e6e76"}, {"iron", "#58585f"},
	},
}

var palettes = map[string]Palette{
	catppuccin.Name: catppuccin,
	stealth.Name:    stealth,
	neonDrive.Name:  neonDrive,
	phosphor.Name:   phosphor,
	graphite.Name:   graphite,
}
