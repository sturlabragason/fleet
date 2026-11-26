package theme

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeThemes(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		user = map[string]Palette{}
		apply(lane)
	})
	return dir
}

func TestLoadDir(t *testing.T) {
	dir := writeThemes(t, map[string]string{
		"mine.toml": "base = \"catppuccin\"\nlabel = \"Mine\"\n[overrides]\nwaiting = \"#FFD75F\"\n",
		"bad.toml": "colour = \"x\"\n[overrides]\nwaitng = \"#ffffff\"\nqueued = \"#ffffff\"\n" +
			"error = \"red\"\nworking = \"#222222\"\n[extra]\n",
		"lane.toml":  "[overrides]\nwaiting = \"#ffffff\"\n",
		"notes.txt":  "not a theme",
		"wrong.toml": "base = \"nope\"\n",
		"Twin.toml":  "",
		"twin.toml":  "",
	})
	probs, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := User(); !slices.Equal(got, []string{"bad", "mine", "twin", "wrong"}) {
		t.Errorf("user themes %v", got)
	}
	if len(probs["mine"]) != 0 {
		t.Errorf("mine: unexpected problems %v", probs["mine"])
	}
	for _, tc := range []struct{ theme, want string }{
		{"bad", `unknown key "colour"`},
		{"bad", `unknown role "waitng"`},
		{"bad", `queued is a status, drawn with role "gone"`},
		{"bad", `error: "red" is not a #rrggbb colour`},
		{"bad", "working #222222 is 1.1:1"},
		{"bad", "unknown section [extra]"},
		{"lane", "name of a built-in theme"},
		{"wrong", `unknown base "nope"`},
		{"twin", "differing only in case"},
	} {
		if !strings.Contains(strings.Join(probs[tc.theme], "\n"), tc.want) {
			t.Errorf("%s: problems %q lack %q", tc.theme, probs[tc.theme], tc.want)
		}
	}
	if err := Load("mine"); err != nil {
		t.Fatal(err)
	}
	if Waiting != "#ffd75f" || Base != catppuccin.Base || Active().Label != "Mine" {
		t.Errorf("mine: waiting %s base %s label %q", Waiting, Base, Active().Label)
	}
	if b, _ := Get("bad"); b.Error != lane.Error || b.Working != "#222222" {
		t.Errorf("bad: a bad colour keeps the base, a low contrast is drawn as written: error %s working %s", b.Error, b.Working)
	}
	if lane.Waiting == "#ffffff" || palettes["lane"].Waiting == "#ffffff" {
		t.Error("a user file changed a built-in palette")
	}
}

func TestLoadDirMissing(t *testing.T) {
	writeThemes(t, nil)
	probs, err := LoadDir(filepath.Join(t.TempDir(), "none"))
	if err != nil || len(probs) != 0 || len(User()) != 0 {
		t.Errorf("missing dir: %v %v %v", probs, err, User())
	}
}
