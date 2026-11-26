package theme

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestReloader walks one session: each step writes a file (or the config
// name), then calls Check twice, since a change applies only once it held
// still for one call.
func TestReloader(t *testing.T) {
	dir := writeThemes(t, map[string]string{"mine.toml": "[overrides]\nwaiting = \"#ffff11\"\n"})
	if _, err := LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := Load("mine"); err != nil {
		t.Fatal(err)
	}
	name, nameErr := "mine", error(nil)
	r := &Reloader{Dir: dir, Config: filepath.Join(dir, "missing.toml"), Name: func() (string, error) { return name, nameErr }}
	if ch, w := r.Check(); ch || w != "" {
		t.Fatalf("first check: changed=%v warn=%q, want nothing", ch, w)
	}
	write := func(body string) func() {
		return func() {
			if err := os.WriteFile(filepath.Join(dir, "mine.toml"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	steps := []struct {
		desc        string
		do          func()
		changed     bool
		warn        string // substring; "" wants no warning
		active, hex string // active theme and its waiting colour after
	}{
		{"nothing changed", func() {}, false, "", "mine", "#ffff11"},
		{"edit the active file", write("[overrides]\nwaiting = \"#ffff22\"\n"), true, "", "mine", "#ffff22"},
		{"a parse error keeps the last good theme", write("[overrides\n"), false, "kept mine", "mine", "#ffff22"},
		{"fixed again", write("[overrides]\nwaiting = \"#ffff33\"\n"), true, "", "mine", "#ffff33"},
		{"a bad value loads and warns", write("[overrides]\nwaiting = \"red\"\n"), true, "not a #rrggbb", "mine", lane.Waiting},
		{"config names a built-in", func() { name = "catppuccin"; write("")() }, true, "", "catppuccin", palettes["catppuccin"].Waiting},
		{"config does not parse", func() { nameErr = errors.New("line 2: bad"); write("# touch")() }, false, "line 2: bad", "catppuccin", palettes["catppuccin"].Waiting},
		{"config names an unknown theme", func() { name, nameErr = "nope", nil; write("")() }, false, "unknown theme", "catppuccin", palettes["catppuccin"].Waiting},
	}
	for i, s := range steps {
		s.do()
		bump(t, dir, i+1)
		if ch, w := r.Check(); ch || w != "" {
			t.Fatalf("%s: first check after the change reloaded (changed=%v warn=%q); want it held one tick", s.desc, ch, w)
		}
		ch, w := r.Check()
		if ch != s.changed || (s.warn == "") != (w == "") || !strings.Contains(w, s.warn) {
			t.Errorf("%s: changed=%v warn=%q, want changed=%v warn~%q", s.desc, ch, w, s.changed, s.warn)
		}
		if a := Active(); a.Name != s.active || a.Waiting != s.hex {
			t.Errorf("%s: active %s waiting %s, want %s %s", s.desc, a.Name, a.Waiting, s.active, s.hex)
		}
		if ch, w := r.Check(); ch || w != "" {
			t.Errorf("%s: a third check reloaded again (changed=%v warn=%q)", s.desc, ch, w)
		}
	}
}

// TestReloaderKeepsBrokenActiveListed: the last good palette of a user theme
// whose file broke stays in the picker.
func TestReloaderKeepsBrokenActiveListed(t *testing.T) {
	dir := writeThemes(t, map[string]string{"mine.toml": "[overrides]\nwaiting = \"#ffff11\"\n"})
	if _, err := LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := Load("mine"); err != nil {
		t.Fatal(err)
	}
	r := &Reloader{Dir: dir, Name: func() (string, error) { return "mine", nil }}
	r.Check()
	if err := os.WriteFile(filepath.Join(dir, "mine.toml"), []byte("[overrides\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bump(t, dir, 1)
	r.Check()
	if _, w := r.Check(); !strings.Contains(w, "kept mine") {
		t.Fatalf("warn %q, want kept mine", w)
	}
	if p, ok := Get("mine"); !ok || p.Waiting != "#ffff11" {
		t.Errorf("Get(mine) = %v %v, want the last good palette", p.Waiting, ok)
	}
}

// bump sets every file's mtime n seconds ahead, so a write within the
// clock's granularity still reads as a change.
func bump(t *testing.T, dir string, n int) {
	t.Helper()
	at := time.Now().Add(time.Duration(n) * time.Second)
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		if err := os.Chtimes(filepath.Join(dir, e.Name()), at, at); err != nil {
			t.Fatal(err)
		}
	}
}
