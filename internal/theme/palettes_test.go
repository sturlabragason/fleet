package theme

import (
	"reflect"
	"regexp"
	"testing"
)

var hexRe = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// Every palette fills every role, so switching themes can never leave a role
// empty and render as the terminal's default colour.
func TestPalettesAreComplete(t *testing.T) {
	for _, name := range Names() {
		p := palettes[name]
		if p.Name != name || p.Label == "" {
			t.Errorf("%s: name %q label %q", name, p.Name, p.Label)
		}
		v := reflect.ValueOf(p)
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if f.Type.Kind() != reflect.String || f.Name == "Name" || f.Name == "Label" {
				continue
			}
			if got := v.Field(i).String(); !hexRe.MatchString(got) {
				t.Errorf("%s.%s = %q, want #rrggbb", name, f.Name, got)
			}
		}
		if len(p.Team) != 6 {
			t.Errorf("%s: %d team colours, want 6 (DESIGN §5c)", name, len(p.Team))
		}
		seen := map[string]bool{}
		for _, sw := range p.Team {
			if sw.Name == "" || !hexRe.MatchString(sw.Hex) {
				t.Errorf("%s: team swatch %+v", name, sw)
			}
			if seen[sw.Hex] {
				t.Errorf("%s: team colour %s used twice", name, sw.Hex)
			}
			seen[sw.Hex] = true
			// A team mark must never be mistaken for a status or the ▶ target.
			// Supervisor is deliberately not in this list: Mocha has shipped
			// mauve as both since §5c, and the admiral pane is named, not
			// marked with a ◆.
			for role, hex := range map[string]string{"working": p.Working, "waiting": p.Waiting, "error": p.Error, "stuck": p.Stuck, "select": p.Select} {
				if sw.Hex == hex {
					t.Errorf("%s: team %q is the %s colour", name, sw.Name, role)
				}
			}
		}
	}
}

// Contrast on each theme's own pane background. The floors are the drafts'
// measured numbers rounded down: a palette edit may not regress a role
// (docs/style.md §4).
func TestPaletteContrast(t *testing.T) {
	min := map[string]float64{
		"Text": 11, "Subtext0": 6, "Overlay2": 4.5,
		"Working": 4.5, "Waiting": 7, "Error": 5, "Stuck": 4.5,
		"Select": 5, "Supervisor": 4.5, "WaitingAlt": 5,
		// Idle and Gone recede on purpose and are never body text.
		"Idle": 3.5, "Gone": 1.6,
	}
	for _, name := range Names() {
		p := palettes[name]
		v := reflect.ValueOf(p)
		for role, want := range min {
			got := Contrast(v.FieldByName(role).String(), p.Base)
			if got < want {
				t.Errorf("%s: %s is %.1f:1 on the base, want >= %.1f:1", name, role, got, want)
			}
		}
		// Gone must stay below Idle, and Idle below Text: lightness carries
		// the hierarchy, so it survives any colour vision.
		if Contrast(p.Gone, p.Base) >= Contrast(p.Idle, p.Base) {
			t.Errorf("%s: gone is not dimmer than idle", name)
		}
		if Contrast(p.Idle, p.Base) >= Contrast(p.Text, p.Base) {
			t.Errorf("%s: idle is not dimmer than text", name)
		}
		// Unfocused panes dim exactly one step: Overlay2 sits between the
		// resting Overlay1 and Text (ADR-37).
		if c2, c1, ct := Contrast(p.Overlay2, p.Base), Contrast(p.Overlay1, p.Base), Contrast(p.Text, p.Base); !(c1 < c2 && c2 < ct) {
			t.Errorf("%s: overlay1 %.1f, overlay2 %.1f, text %.1f: not one step apart", name, c1, c2, ct)
		}
	}
}

// The select colour is the ▶ target and nothing else (ADR-37), so no status
// may share it.
func TestSelectIsItsOwnColour(t *testing.T) {
	for _, name := range Names() {
		p := palettes[name]
		for role, hex := range map[string]string{"working": p.Working, "waiting": p.Waiting, "error": p.Error, "stuck": p.Stuck, "idle": p.Idle, "gone": p.Gone} {
			if p.Select == hex {
				t.Errorf("%s: select is also %s", name, role)
			}
		}
	}
}

func TestLoad(t *testing.T) {
	t.Cleanup(func() { apply(catppuccin) })
	if err := Load("bogus"); err == nil {
		t.Error("an unknown theme must be an error, not a silent fallback")
	}
	if Select != catppuccin.Select {
		t.Error("a failed Load must leave the palette alone")
	}
	for _, name := range Names() {
		if err := Load(name); err != nil {
			t.Fatalf("Load(%q): %v", name, err)
		}
		if Active().Name != name || Select != palettes[name].Select || Muted != palettes[name].Overlay1 {
			t.Errorf("Load(%q): roles not applied", name)
		}
		if TeamHex(TeamPalette[0].Name) != TeamPalette[0].Hex {
			t.Errorf("Load(%q): TeamHex disagrees with TeamPalette", name)
		}
	}
	if err := Load(""); err != nil || Active().Name != Default {
		t.Errorf(`Load("") = %v, active %q, want the default`, err, Active().Name)
	}
}

// A cached colour (a lipgloss style, a tmux format string) must be rebuilt
// when the theme changes.
func TestOnChange(t *testing.T) {
	t.Cleanup(func() { apply(catppuccin) })
	var got string
	n := len(hooks)
	OnChange(func() { got = Select })
	t.Cleanup(func() { hooks = hooks[:n] })
	if got != catppuccin.Select {
		t.Errorf("OnChange did not run once on registration: %q", got)
	}
	if err := Load("phosphor"); err != nil {
		t.Fatal(err)
	}
	if got != phosphor.Select {
		t.Errorf("hook saw %q, want %q", got, phosphor.Select)
	}
}

func TestStatus(t *testing.T) {
	cases := map[string]string{"working": catppuccin.Working, "waiting": catppuccin.Waiting, "error": catppuccin.Error, "stuck": catppuccin.Stuck, "bogus": catppuccin.Idle}
	for st, want := range cases {
		if got := Status(st); got != want {
			t.Errorf("Status(%q) = %s, want %s", st, got, want)
		}
	}
}
