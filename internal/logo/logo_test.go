package logo

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sturlabragason/fleet/internal/theme"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestGolden pins every drawing, Unicode and ASCII, to testdata: a change to
// the art is a change to these files, looked at in review.
func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		name string
		size Size
	}{{"line", Line}, {"mark", Mark}, {"splash", Splash}} {
		for _, asc := range []bool{false, true} {
			name := tc.name
			if asc {
				name += ".ascii"
			}
			t.Run(name, func(t *testing.T) {
				got := strings.Join(Plain(tc.size, asc), "\n") + "\n"
				path := filepath.Join("testdata", name+".txt")
				if *update {
					if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if got != string(want) {
					t.Errorf("%s differs from %s (go test -update rewrites it):\n%s", name, path, got)
				}
			})
		}
	}
}

// TestShape: each drawing is one block no wider than 80 columns, its role
// grid matches it cell for cell, and the ASCII form is pure ASCII.
func TestShape(t *testing.T) {
	for _, s := range []Size{Line, Mark, Splash} {
		a := s.art()
		if len(a.roles) != len(a.rows) {
			t.Fatalf("size %d: %d rows, %d role rows", s, len(a.rows), len(a.roles))
		}
		for i, r := range a.rows {
			if w := ansi.StringWidth(r); w != s.Width() || w > 80 {
				t.Errorf("size %d row %d: width %d, want %d (≤ 80)", s, i, w, s.Width())
			}
			cells, roles := []rune(r), []rune(a.roles[i])
			if len(cells) != len(roles) {
				t.Errorf("size %d row %d: %d cells, %d roles", s, i, len(cells), len(roles))
				continue
			}
			for j, c := range cells {
				if (c == ' ') != (roles[j] == ' ') {
					t.Errorf("size %d row %d col %d: cell %q has role %q", s, i, j, c, roles[j])
				}
			}
		}
		for i, r := range Plain(s, true) {
			for _, c := range r {
				if c > 0x7e {
					t.Errorf("size %d ascii row %d: non-ASCII %q", s, i, c)
				}
			}
		}
	}
}

func TestFit(t *testing.T) {
	for _, tc := range []struct {
		w, h int
		want Size
		ok   bool
	}{
		{80, 24, Splash, true},
		{Splash.Width(), Splash.Height(), Splash, true},
		{Splash.Width() - 1, 24, Mark, true},
		{80, Splash.Height() - 1, Mark, true},
		{Mark.Width(), 2, Line, true},
		{Line.Width() - 1, 1, Line, false},
	} {
		if got, ok := Fit(tc.w, tc.h); got != tc.want || ok != tc.ok {
			t.Errorf("Fit(%d, %d) = %d, %v; want %d, %v", tc.w, tc.h, got, ok, tc.want, tc.ok)
		}
	}
}

// TestRolesFollowTheme: the accent is the active theme's Select, read at
// render time, so a theme change recolours the mark.
func TestRolesFollowTheme(t *testing.T) {
	t.Cleanup(func() { _ = theme.Load(theme.Default) })
	for _, name := range theme.Names() {
		if err := theme.Load(name); err != nil {
			t.Fatal(err)
		}
		if got := style('a').GetForeground(); got != lipgloss.Color(theme.Select) {
			t.Errorf("%s: accent %v, want Select %s", name, got, theme.Select)
		}
		if got := style('w'); got.GetForeground() != lipgloss.Color(theme.Text) || !got.GetBold() {
			t.Errorf("%s: word not bold Text", name)
		}
	}
}

func TestASCII(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{"TERM": "xterm-256color", "LANG": "en_US.UTF-8"}, false},
		{map[string]string{"TERM": "dumb", "LANG": "en_US.UTF-8"}, true},
		{map[string]string{"TERM": "xterm", "LANG": "C"}, true},
		{map[string]string{"TERM": "xterm", "LANG": "en_US.UTF-8", "LC_ALL": "C"}, true},
		{map[string]string{"TERM": "tmux-256color", "TMUX": "/tmp/s,1,0"}, false},
	} {
		if got := ASCII(func(k string) string { return tc.env[k] }); got != tc.want {
			t.Errorf("ASCII(%v) = %v, want %v", tc.env, got, tc.want)
		}
	}
}

// TestHead: the mark heads a list only when it costs no row of the list.
func TestHead(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LC_ALL", "C.UTF-8")
	list := []string{"a", "b"}
	for _, tc := range []struct {
		w, h, want int
	}{{80, 2 + HeadRows, 2 + HeadRows}, {80, 1 + HeadRows, 2}, {Mark.Width() - 1, 40, 2}} {
		got := Head(list, tc.w, tc.h)
		if len(got) != tc.want {
			t.Errorf("Head(%d×%d) = %d rows, want %d", tc.w, tc.h, len(got), tc.want)
		}
		if got[len(got)-1] != "b" {
			t.Errorf("Head(%d×%d) lost the list: %q", tc.w, tc.h, got)
		}
	}
}
