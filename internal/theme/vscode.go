package theme

import (
	"fmt"
	"math"
	"strconv"
)

// vscodeTheme is a VS Code colour theme as its extension publishes it: the
// editor ground and ink, the accent, and the terminal ANSI hues (ADR-174).
// The values are copied verbatim, alpha included; fromVSCode turns them into
// roles. An empty bright hue means the theme has none of its own.
type vscodeTheme struct {
	name, label string
	bg, fg      string
	accent      string // the ▶ target: the theme's own accent, never its yellow
	red, green  string
	yellow      string
	orange      string // second waiting frame; "" derives it from yellow and red
	blue        string
	magenta     string
	cyan        string

	brightBlue, brightMagenta, brightCyan string
}

// white and black are the poles liftTo pushes toward.
const white, black = "#ffffff", "#000000"

// fromVSCode maps a VS Code theme onto fleet's roles. Each role is lifted only
// as far as its contrast floor on the theme's own base (docs/style.md §4,
// palettes_test.go), toward white on a dark ground and toward black on a light
// one, so every theme keeps its hues where they are legible and stays legible
// where they are not. Crust is the ink on coloured badges (the status-line
// mark, the window tab, owner chips), so on a light theme it is the light end,
// not the darkest ground.
func fromVSCode(v vscodeTheme) Palette {
	bg := solid(v.bg, black)
	light := luminance(bg) > 0.18
	pole := white
	if light {
		pole = black
	}
	floor := func(hex string, ratio float64) string { return liftTo(hex, bg, pole, ratio) }
	lift := func(hex string, ratio float64) string { return floor(solid(hex, bg), ratio) }

	text := lift(v.fg, 11)
	p := Palette{
		Name: v.name, Label: v.label,
		Base:     bg,
		Surface0: mix(bg, text, 0.10), Surface1: mix(bg, text, 0.18), Surface2: mix(bg, text, 0.30),
		Text:     text,
		Subtext0: floor(mix(text, bg, 0.20), 6),
		Overlay2: floor(mix(text, bg, 0.35), 4.5),
		Overlay1: floor(mix(text, bg, 0.50), 3.5),
		Overlay0: mix(text, bg, 0.62),
		Working:  lift(v.green, 4.5),
		Waiting:  lift(v.yellow, 7),
		Error:    lift(v.red, 5),
		Select:   lift(v.accent, 5),
	}
	p.Mantle, p.Crust = mix(bg, black, 0.25), mix(bg, black, 0.55)
	if light {
		p.Mantle, p.Crust = mix(bg, text, 0.06), white
	}
	p.WorkingDim = mix(p.Working, bg, 0.45)
	orange := mix(solid(v.yellow, bg), solid(v.red, bg), 0.35)
	if v.orange != "" {
		orange = solid(v.orange, bg)
	}
	p.WaitingAlt = floor(orange, 5)
	p.Stuck = floor(mix(solid(v.red, bg), text, 0.40), 4.5)
	p.Idle = p.Overlay1
	p.Gone = floor(p.Surface2, 1.6)
	p.Supervisor = floor(mix(text, pole, 0.50), 4.5)
	p.Team = vscodeTeam(v, p, pole)
	return p
}

// vscodeTeam picks six team colours from the theme's blues, magentas and
// cyans, skipping any that would pass for a status, the ▶ target, the
// supervisor or an earlier team colour.
func vscodeTeam(v vscodeTheme, p Palette, pole string) []Swatch {
	bg := p.Base
	blue, magenta, cyan := solid(v.blue, bg), solid(v.magenta, bg), solid(v.cyan, bg)
	violet := mix(blue, magenta, 0.5)
	cands := []Swatch{
		{"blue", blue}, {"magenta", magenta}, {"cyan", cyan},
		{"sky", v.brightBlue}, {"pink", v.brightMagenta}, {"aqua", v.brightCyan},
		{"violet", violet}, {"azure", mix(blue, cyan, 0.5)},
		{"pale-blue", mix(blue, p.Text, 0.40)}, {"pale-magenta", mix(magenta, p.Text, 0.40)}, {"pale-cyan", mix(cyan, p.Text, 0.40)},
		{"orchid", mix(magenta, p.Error, 0.5)}, {"pale-violet", mix(violet, p.Text, 0.40)},
		// Muted themes (Nord) run out of hues: the last resort is lightness
		// steps toward the ink, as Phosphor and Graphite do (ADR-55).
		{"mist-blue", mix(blue, p.Text, 0.70)}, {"mist-magenta", mix(magenta, p.Text, 0.70)}, {"mist-cyan", mix(cyan, p.Text, 0.70)},
	}
	taken := []string{p.Working, p.Waiting, p.WaitingAlt, p.Error, p.Stuck, p.Select, p.Supervisor, p.Idle, p.Gone}
	var team []Swatch
	for _, sw := range cands {
		if sw.Hex == "" {
			continue
		}
		// A ◆ mark needs 3:1 (WCAG 1.4.11); two team colours are admiral
		// badges, so Crust ink on them needs 4.5:1.
		hex := liftTo(liftTo(solid(sw.Hex, bg), bg, pole, 3), p.Crust, pole, 4.5)
		if nearAny(hex, taken) {
			continue
		}
		team = append(team, Swatch{sw.Name, hex})
		taken = append(taken, hex)
		if len(team) == 6 {
			break
		}
	}
	return team
}

func nearAny(hex string, others []string) bool {
	for _, o := range others {
		if distance(hex, o) < 30 { // RGB distance: two marks must not pass for one
			return true
		}
	}
	return false
}

// liftTo moves c toward pole in small steps until it reaches floor:1 on bg.
func liftTo(c, bg, pole string, floor float64) string {
	for t := 0.0; t <= 1; t += 0.02 {
		if m := mix(c, pole, t); Contrast(m, bg) >= floor {
			return m
		}
	}
	return pole
}

type rgb struct{ r, g, b float64 }

func parse(hex string) (rgb, float64) {
	if len(hex) == 4 { // #rgb
		hex = "#" + string([]byte{hex[1], hex[1], hex[2], hex[2], hex[3], hex[3]})
	}
	ch := func(i int) float64 {
		v, err := strconv.ParseUint(hex[i:i+2], 16, 8)
		if err != nil {
			panic(fmt.Sprintf("theme: bad colour %q", hex)) // only ever fed the literals in vscode_palettes.go
		}
		return float64(v)
	}
	alpha := 255.0
	if len(hex) == 9 {
		alpha = ch(7)
	}
	return rgb{ch(1), ch(3), ch(5)}, alpha / 255
}

func format(c rgb) string {
	b := func(v float64) int { return int(math.Round(math.Max(0, math.Min(255, v)))) }
	return fmt.Sprintf("#%02x%02x%02x", b(c.r), b(c.g), b(c.b))
}

// solid flattens a colour with alpha onto ground, as the editor paints it.
func solid(hex, ground string) string {
	c, a := parse(hex)
	return mix(format(c), ground, 1-a)
}

// mix is a + (b-a)*t in sRGB.
func mix(a, b string, t float64) string {
	x, _ := parse(a)
	y, _ := parse(b)
	return format(rgb{x.r + (y.r-x.r)*t, x.g + (y.g-x.g)*t, x.b + (y.b-x.b)*t})
}

func distance(a, b string) float64 {
	x, _ := parse(a)
	y, _ := parse(b)
	return math.Sqrt((x.r-y.r)*(x.r-y.r) + (x.g-y.g)*(x.g-y.g) + (x.b-y.b)*(x.b-y.b))
}
