package prefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetDefault(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"empty file", "", "[defaults]\ntheme = \"nord\"\n"},
		{"no defaults table", "[profile.x]\ncmd = \"y\"", "[profile.x]\ncmd = \"y\"\n\n[defaults]\ntheme = \"nord\"\n"},
		{"replaces in place", "[defaults]\nmode = \"auto\"\ntheme = \"lane\"\n[x]\ntheme = \"keep\"\n", "[defaults]\nmode = \"auto\"\ntheme = \"nord\"\n[x]\ntheme = \"keep\"\n"},
		{"adds under the header", "# c\n[defaults] # d\nmode = \"auto\"\n", "# c\n[defaults] # d\ntheme = \"nord\"\nmode = \"auto\"\n"},
		{"other table's key untouched", "[x]\ntheme = \"keep\"\n[defaults]\n", "[x]\ntheme = \"keep\"\n[defaults]\ntheme = \"nord\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := setDefault(tc.in, "theme", `"nord"`); got != tc.want {
				t.Fatalf("got\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestSetThemeThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.toml")
	if err := os.WriteFile(target, []byte("[defaults]\ntheme = \"lane\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_CONFIG", link)
	if err := SetTheme("dracula"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link replaced: %v %v", fi, err)
	}
	b, _ := os.ReadFile(target)
	st, _ := os.Stat(target)
	if string(b) != "[defaults]\ntheme = \"dracula\"\n" || st.Mode().Perm() != 0o600 {
		t.Fatalf("got %q mode %v", b, st.Mode().Perm())
	}
	if p, err := Load(); err != nil || p.Theme() != "dracula" {
		t.Fatalf("read back %v %v", p, err)
	}
}
