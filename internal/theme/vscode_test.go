package theme

import "testing"

// Golden for the VS Code mapping (ADR-174): one dark theme whose accent had
// to move off its yellow, and one light theme, whose badge ink flips to white.
func TestFromVSCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		want map[string]string
	}{
		{"shades-of-purple", map[string]string{
			"Base": "#2d2b55", "Crust": "#141326", "Text": "#ffffff", "Working": "#3ad900", "Waiting": "#fad000",
			"Error": "#ee8080", "Select": "#c485ff", "Supervisor": "#ffffff", "Team0": "#8264fe",
		}},
		{"github-light", map[string]string{
			"Base": "#ffffff", "Crust": "#ffffff", "Text": "#1f2328", "Working": "#1a7f37", "Waiting": "#785000",
			"Error": "#cf222e", "Select": "#0969da", "Supervisor": "#101214", "Team0": "#8250df",
		}},
	} {
		p := palettes[tc.name]
		got := map[string]string{
			"Base": p.Base, "Crust": p.Crust, "Text": p.Text, "Working": p.Working, "Waiting": p.Waiting,
			"Error": p.Error, "Select": p.Select, "Supervisor": p.Supervisor, "Team0": p.Team[0].Hex,
		}
		for role, hex := range tc.want {
			if got[role] != hex {
				t.Errorf("%s.%s = %s, want %s", tc.name, role, got[role], hex)
			}
		}
	}
}

// Badges print Crust on a coloured ground (the status-line mark on
// Supervisor, the window tab on Select, owner chips on an admiral colour, the
// where-chip on Subtext0): it must read on all of them in every mapped theme.
func TestBadgeInkIsLegible(t *testing.T) {
	defer Load(Active().Name)
	for _, v := range vscodeThemes {
		name := v.name
		if err := Load(name); err != nil {
			t.Fatal(err)
		}
		p := palettes[name]
		for role, bg := range map[string]string{"Supervisor": p.Supervisor, "Select": p.Select, "Subtext0": p.Subtext0,
			"admiral 1": AdmiralColor(1), "admiral 2": AdmiralColor(2)} {
			if c := Contrast(p.Crust, bg); c < 4.5 {
				t.Errorf("%s: crust on %s is %.1f:1, want >= 4.5:1", name, role, c)
			}
		}
	}
}
