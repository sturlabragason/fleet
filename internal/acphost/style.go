package acphost

import (
	"image/color"
	"strconv"
	"strings"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/sturlabragason/fleet/internal/theme"
)

// The pane's look (ADR-84): colours come from the active fleet theme
// (internal/theme, [defaults] theme in config.toml), so the pane matches
// the wall. Nothing blinks or moves: every mark is a static glyph in a
// steady colour.

// sty is one run of text's look. Colours are theme hex values; "" is the
// terminal's default.
type sty struct {
	fg, bg                     string
	bold, italic, under, faint bool
}

// painter turns styled text into escape sequences for one colour profile.
type painter struct{ p colorprofile.Profile }

// ColorProfile is what the pane can show, from the environment: NO_COLOR
// keeps bold and italic but drops colour, TERM=dumb drops both. Inside tmux
// a 256-colour TERM still gets true colour: tmux maps it down itself when
// the outer terminal can't show it.
func ColorProfile(env []string) colorprofile.Profile {
	p := colorprofile.Env(env)
	if p == colorprofile.ANSI256 {
		for _, kv := range env {
			if strings.HasPrefix(kv, "TMUX=") && len(kv) > len("TMUX=") {
				return colorprofile.TrueColor
			}
		}
	}
	return p
}

func (pt painter) color() bool { return pt.p > colorprofile.Ascii }

// paint wraps text in s's escape sequences and a reset.
func (pt painter) paint(s sty, text string) string {
	if text == "" || pt.p <= colorprofile.NoTTY {
		return text
	}
	var st ansi.Style
	if s.bold {
		st = st.Bold()
	}
	if s.faint {
		st = st.Faint()
	}
	if s.italic {
		st = st.Italic()
	}
	if s.under {
		st = st.Underline()
	}
	if pt.color() {
		if c := pt.conv(s.fg); c != nil {
			st = st.ForegroundColor(c)
		}
		if c := pt.conv(s.bg); c != nil {
			st = st.BackgroundColor(c)
		}
	}
	if len(st) == 0 {
		return text
	}
	return st.String() + text + "\x1b[m"
}

func (pt painter) conv(hex string) color.Color {
	c, ok := parseHex(hex)
	if !ok {
		return nil
	}
	return pt.p.Convert(c)
}

func parseHex(s string) (color.RGBA, bool) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return color.RGBA{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.RGBA{}, false
	}
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}, true
}

// palette is the pane's roles, read from the active theme when the console
// starts. Every colour is a meaning (docs/style.md §4): status colours for
// status, Supervisor for the person giving orders, inks for hierarchy.
type palette struct {
	text, sub, dim, faint, rule, band, head string
	working, waiting, err, you              string
}

func themePalette() palette {
	return palette{
		text: theme.Text, sub: theme.Subtext0, dim: theme.Overlay1, faint: theme.Overlay0,
		rule: theme.Surface1, band: theme.Surface0, head: theme.Mantle,
		working: theme.Working, waiting: theme.Waiting, err: theme.Error, you: theme.Supervisor,
	}
}

// Static glyphs. A tool call has one line whose glyph changes in place.
const (
	glyphPending = "○"
	glyphDone    = "✓"
	glyphFailed  = "✗"
	glyphAsk     = "?"
	glyphYou     = "›"
	glyphThought = "∴"
	glyphNote    = "·"
)

// statusGlyph is the header's mark for the agent's status, as on the wall
// (internal/ui): colour tells working from idle, never motion.
func statusGlyph(st string) string {
	switch st {
	case "waiting":
		return "●"
	case "error":
		return "✖"
	case "stuck":
		return "⧗"
	case "gone":
		return "·"
	}
	return "○"
}

func (pl palette) status(st string) string {
	switch st {
	case "working":
		return pl.working
	case "waiting":
		return pl.waiting
	case "error", "stuck":
		return pl.err
	}
	return pl.dim
}

func (pl palette) tool(st string) (string, string) {
	switch st {
	case "completed":
		return glyphDone, pl.working
	case "failed":
		return glyphFailed, pl.err
	case "in_progress":
		return glyphPending, pl.working
	}
	return glyphPending, pl.dim
}
