package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/theme"
)

func TestCheckThemes(t *testing.T) {
	t.Cleanup(func() { _, _ = theme.LoadDir(t.TempDir()) })
	tests := []struct {
		name, body, level, want string
		shown                   bool
	}{
		{"none", "", "", "", false},
		{"ok.toml", "[overrides]\nwaiting = \"#ffd75f\"\n", levelOK, "1 in", true},
		{"bad.toml", "[overrides]\nwaitng = \"#ffd75f\"\n", levelWarn, `bad.toml: unknown role "waitng"`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.body != "" {
				if err := os.WriteFile(filepath.Join(dir, tc.name), []byte(tc.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			c, shown := checkThemes(doctorEnv{}, dir)
			if shown != tc.shown || c.Level != tc.level || !strings.Contains(c.Detail, tc.want) {
				t.Errorf("got %v %+v, want shown %v level %s detail with %q", shown, c, tc.shown, tc.level, tc.want)
			}
		})
	}
}

// A theme file that does not parse must not stop fleet: the default draws
// and fleet doctor can say why.
func TestLoadThemeBrokenFileFallsBack(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(dir, "config.toml"))
	t.Cleanup(func() { _, _ = theme.LoadDir(t.TempDir()); _ = theme.Load("") })
	if err := os.MkdirAll(filepath.Join(dir, "themes"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		"config.toml":      "[defaults]\ntheme = \"mine\"\n",
		"themes/mine.toml": "[overrides]\nwaiting\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := loadTheme(); err != nil || theme.Active().Name != theme.Default {
		t.Errorf("loadTheme: %v, active %s, want the default", err, theme.Active().Name)
	}
}
