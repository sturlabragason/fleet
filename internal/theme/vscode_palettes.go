package theme

// The 20 most-installed VS Code colour themes on the Marketplace, 2026-11-19
// (ADR-174; licences and authors in THEMES.md). Catppuccin is one of them and
// ships hand-tuned as "catppuccin"; GitHub ships its dark and light default.
// Every hex is the extension's own: editor.background, editor.foreground,
// terminal.ansi*, or a token colour where the terminal set has none.
var vscodeThemes = []vscodeTheme{
	{name: "github-dark", label: "GitHub Dark", bg: "#0d1117", fg: "#e6edf3", accent: "#58a6ff",
		red: "#ff7b72", green: "#3fb950", yellow: "#d29922", orange: "#e3b341",
		blue: "#58a6ff", magenta: "#bc8cff", cyan: "#39c5cf", brightBlue: "#79c0ff", brightMagenta: "#d2a8ff", brightCyan: "#56d4dd"},
	{name: "github-light", label: "GitHub Light", bg: "#ffffff", fg: "#1f2328", accent: "#0969da",
		red: "#cf222e", green: "#1a7f37", yellow: "#9a6700", orange: "#bc4c00",
		blue: "#0969da", magenta: "#8250df", cyan: "#1b7c83", brightBlue: "#218bff", brightMagenta: "#a475f9", brightCyan: "#3192aa"},
	{name: "one-dark-pro", label: "One Dark Pro", bg: "#282c34", fg: "#abb2bf", accent: "#4d78cc",
		red: "#e05561", green: "#8cc265", yellow: "#e5c07b", orange: "#d19a66",
		blue: "#4aa5f0", magenta: "#c162de", cyan: "#42b3c2", brightBlue: "#4dc4ff", brightMagenta: "#de73ff", brightCyan: "#4cd1e0"},
	{name: "dracula", label: "Dracula", bg: "#282a36", fg: "#f8f8f2", accent: "#bd93f9",
		red: "#ff5555", green: "#50fa7b", yellow: "#f1fa8c", orange: "#ffb86c",
		blue: "#bd93f9", magenta: "#ff79c6", cyan: "#8be9fd", brightBlue: "#d6acff", brightMagenta: "#ff92df", brightCyan: "#a4ffff"},
	{name: "atom-one-dark", label: "Atom One Dark", bg: "#282c34", fg: "#abb2bf", accent: "#528bff",
		red: "#e06c75", green: "#98c379", yellow: "#e5c07b", orange: "#d19a66",
		blue: "#61afef", magenta: "#c678dd", cyan: "#56b6c2"},
	// Ayu's own accent is its yellow; the ▶ target takes its blue so it can't
	// pass for waiting.
	{name: "ayu", label: "Ayu Dark", bg: "#10141c", fg: "#fcf8ecbd", accent: "#59c2ff",
		red: "#f06b73", green: "#70bf56", yellow: "#fdb04c", orange: "#ff8f40",
		blue: "#4fbfff", magenta: "#d0a1ff", cyan: "#93e2c8", brightMagenta: "#d2a6ff", brightCyan: "#95e6cb"},
	{name: "winter-is-coming", label: "Winter is Coming", bg: "#011627", fg: "#a7dbf7", accent: "#219fd5",
		red: "#ef5350", green: "#22da6e", yellow: "#ffca28",
		blue: "#219fd5", magenta: "#c792ea", cyan: "#80cbc4", brightBlue: "#82aaff", brightCyan: "#7fdbca"},
	{name: "night-owl", label: "Night Owl", bg: "#011627", fg: "#d6deeb", accent: "#c792ea",
		red: "#ef5350", green: "#22da6e", yellow: "#ffeb95", orange: "#f78c6c",
		blue: "#82aaff", magenta: "#c792ea", cyan: "#21c7a8", brightCyan: "#7fdbca"},
	{name: "tokyo-night", label: "Tokyo Night", bg: "#1a1b26", fg: "#a9b1d6", accent: "#7aa2f7",
		red: "#f7768e", green: "#9ece6a", yellow: "#e0af68", orange: "#ff9e64",
		blue: "#7aa2f7", magenta: "#bb9af7", cyan: "#7dcfff", brightCyan: "#2ac3de"},
	{name: "one-monokai", label: "One Monokai", bg: "#282c34", fg: "#abb2bf", accent: "#528bff",
		red: "#e06c75", green: "#98c379", yellow: "#e5c07b", orange: "#d19a66",
		blue: "#528bff", magenta: "#c678dd", cyan: "#56b6c2"},
	{name: "palenight", label: "Palenight", bg: "#292d3e", fg: "#bfc7d5", accent: "#c792ea",
		red: "#ff5572", green: "#c3e88d", yellow: "#ffcb6b", orange: "#f78c6c",
		blue: "#82aaff", magenta: "#c792ea", cyan: "#89ddff"},
	{name: "synthwave-84", label: "SynthWave '84", bg: "#262335", fg: "#ffffff", accent: "#ff7edb",
		red: "#fe4450", green: "#72f1b8", yellow: "#fede5d", orange: "#ff8b39",
		blue: "#03edf9", magenta: "#ff7edb", cyan: "#36f9f6"},
	// Shades of Purple's accent is its yellow, which is waiting here; the ▶
	// target takes the theme's purple selection colour instead.
	{name: "shades-of-purple", label: "Shades of Purple", bg: "#2d2b55", fg: "#ffffff", accent: "#b362ff",
		red: "#ec3a37f5", green: "#3ad900", yellow: "#fad000", orange: "#ff9d00",
		blue: "#7857fe", magenta: "#ff2c70", cyan: "#80fcff", brightBlue: "#6943ff", brightMagenta: "#fb94ff"},
	// Cobalt2's accent is yellow too; the ▶ target takes its button blue.
	{name: "cobalt2", label: "Cobalt2", bg: "#193549", fg: "#ffffff", accent: "#0088ff",
		red: "#ff628c", green: "#3ad900", yellow: "#ffc600", orange: "#ff9d00",
		blue: "#0088ff", magenta: "#fb94ff", cyan: "#80fcff"},
	{name: "andromeda", label: "Andromeda", bg: "#23262e", fg: "#d5ced9", accent: "#00e8c6",
		red: "#ee5d43", green: "#96e072", yellow: "#ffe66d", orange: "#f39c12",
		blue: "#7cb7ff", magenta: "#ff00aa", cyan: "#00e8c6"},
	{name: "atom-one-light", label: "Atom One Light", bg: "#fafafa", fg: "#383a42", accent: "#526fff",
		red: "#e45649", green: "#50a14f", yellow: "#c18401", orange: "#986801",
		blue: "#4078f2", magenta: "#a626a4", cyan: "#0184bc"},
	{name: "noctis", label: "Noctis", bg: "#052529", fg: "#b2cacd", accent: "#40d4e7",
		red: "#e66533", green: "#49e9a6", yellow: "#e4b781", orange: "#e69533",
		blue: "#49ace9", magenta: "#df769b", cyan: "#49d6e9", brightBlue: "#60b6eb", brightMagenta: "#e798b3", brightCyan: "#60dbeb"},
	{name: "nord", label: "Nord", bg: "#2e3440", fg: "#d8dee9", accent: "#88c0d0",
		red: "#bf616a", green: "#a3be8c", yellow: "#ebcb8b", orange: "#d08770",
		blue: "#81a1c1", magenta: "#b48ead", cyan: "#8fbcbb", brightBlue: "#5e81ac"},
	{name: "gruvbox", label: "Gruvbox Dark", bg: "#282828", fg: "#ebdbb2", accent: "#83a598",
		red: "#fb4934", green: "#b8bb26", yellow: "#fabd2f", orange: "#fe8019",
		blue: "#458588", magenta: "#b16286", cyan: "#689d6a", brightMagenta: "#d3869b", brightCyan: "#8ec07c"},
	{name: "material", label: "Sublime Material", bg: "#252526", fg: "#eeffff", accent: "#80cbc4",
		red: "#ff5370", green: "#c3e88d", yellow: "#ffcb6b", orange: "#f78c6c",
		blue: "#82aaff", magenta: "#c792ea", cyan: "#89ddff"},
}

func init() {
	for _, v := range vscodeThemes {
		palettes[v.name] = fromVSCode(v)
	}
}
