package main

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/theme"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// menuItems is the menu's item names in order, "-" for a separator.
func menuItems(t *testing.T, args []string) (names, keys []string) {
	t.Helper()
	i := 0
	for i < len(args) && args[i] != "-y" {
		i++
	}
	for i += 2; i < len(args); {
		if args[i] == "" {
			names, i = append(names, "-"), i+1
			continue
		}
		names, keys = append(names, args[i]), append(keys, args[i+1])
		i += 3
	}
	return names, keys
}

func TestMenuArgs(t *testing.T) {
	cases := []struct {
		st   menuState
		want string
	}{
		{menuState{onWall: true, hasTarget: true}, "Put in ▶ pane,Add as a new pane,Preview,-,Pin,Take off the wall"},
		{menuState{onWall: false, hasTarget: true}, "Put in ▶ pane,Add as a new pane,Preview,-,Pin"},
		{menuState{onWall: true, hasTarget: false, pinned: true}, "Add to wall,Add as a new pane,Preview,-,Unpin,Take off the wall"},
		{menuState{}, "Add to wall,Add as a new pane,Preview,-,Pin"},
		// A pinned ▶ pane is never replaced: the card goes onto the wall.
		{menuState{onWall: false, hasTarget: true, targetPinned: true}, "Add to wall,Add as a new pane,Preview,-,Pin"},
	}
	for _, c := range cases {
		args := menuArgs("'/usr/bin/fleet'", "bravo", 10, 5, c.st)
		// -M: the menu takes the mouse, else clicking an item does nothing.
		if args[0] != "display-menu" || args[1] != "-M" || args[2] != "-T" || args[3] != " bravo " || args[4] != "-x" || args[5] != "10" {
			t.Errorf("head %q", args[:6])
		}
		names, keys := menuItems(t, args)
		if got := strings.Join(names, ","); got != c.want {
			t.Errorf("%+v: items %s, want %s", c.st, got, c.want)
		}
		// -y is the bottom line: the top lands on the pointer row.
		if args[7] != fmt.Sprint(5+len(names)+2) {
			t.Errorf("%+v: -y %s for %d items", c.st, args[7], len(names))
		}
		if keys[0] != "r" || keys[1] != "a" || keys[2] != "Space" || keys[3] != "p" {
			t.Errorf("keys %v", keys)
		}
	}
	all := strings.Join(menuArgs("'/usr/bin/fleet'", "bravo", 1, 1, menuState{onWall: true, hasTarget: true}), "\n")
	for _, want := range []string{
		`'run-shell' '-b' "'/usr/bin/fleet' wall show --keep-focus --notify bravo >/dev/null 2>&1"`,
		`'run-shell' '-b' "'/usr/bin/fleet' wall add --keep-focus --notify bravo >/dev/null 2>&1"`,
		`'run-shell' '-b' "'/usr/bin/fleet' wall pin --notify bravo >/dev/null 2>&1"`,
		`'run-shell' '-b' "'/usr/bin/fleet' wall hide --notify bravo >/dev/null 2>&1"`,
		`'display-popup' '-E'`,
		`"'/usr/bin/fleet' peek --live bravo"`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("menu lacks %q:\n%s", want, all)
		}
	}
}

func TestPreviewPopupArgs(t *testing.T) {
	got := strings.Join(previewPopupArgs("'/usr/bin/fleet'", "bravo", true), "|")
	want := "display-popup|-E|-b|rounded|-w|90%|-h|85%|-T| bravo · preview · ⏎ put in ▶ · q close |'/usr/bin/fleet' peek --live bravo"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if a := previewPopupArgs("x", "bravo", false); a[9] != " bravo · preview · ⏎ add · q close " {
		t.Errorf("no target title %q", a[9])
	}
}

// liveFixture is a captured agent screen (scrubbed): colours, then blank rows.
const liveFixture = "\x1b[32m● working\x1b[0m on the parser\n" +
	"line two is long enough to be cut at twenty cells\n" +
	"\x1b[1mApprove? [y/n]\x1b[0m\n" +
	"\n\n"

func TestLiveViewCropsAndEnterShows(t *testing.T) {
	shows := 0
	m := &liveModel{name: "bravo",
		capture: func() (string, error) { return liveFixture, nil },
		show:    func() error { shows++; return nil }}
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 2})
	if _, cmd := m.Update(liveTick{}); cmd == nil {
		t.Fatal("live view must keep ticking")
	}
	v := m.View()
	lines := strings.Split(v, "\n")
	if len(lines) != 2 {
		t.Fatalf("want the bottom 2 non-blank lines, got %q", v)
	}
	if got := ansi.Strip(lines[0]); got != "line two is long eno" {
		t.Errorf("line 1 %q", got)
	}
	if got := ansi.Strip(lines[1]); got != "Approve? [y/n]" || !strings.Contains(lines[1], "\x1b[1m") {
		t.Errorf("line 2 %q (colours kept)", lines[1])
	}
	for _, l := range lines {
		if ansi.StringWidth(l) > 20 {
			t.Errorf("line wider than 20: %q", l)
		}
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil || shows != 1 {
		t.Errorf("Enter: shows=%d", shows)
	}
	for _, k := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("q")}, {Type: tea.KeyEsc}, {Type: tea.KeySpace}} {
		if _, cmd := m.Update(k); cmd == nil {
			t.Errorf("%s must close", k)
		}
	}
	if shows != 1 {
		t.Error("only Enter shows")
	}
}

func TestLiveViewWithoutAPane(t *testing.T) {
	m := &liveModel{name: "queued1", noPane: true}
	if m.Init() != nil {
		t.Error("no pane: nothing to capture")
	}
	if v := m.View(); v != "queued1 has no pane · any key closes" {
		t.Errorf("view %q", v)
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}); cmd == nil {
		t.Error("any key closes")
	}
}

// A pinned pane's border shows ⚑ in Overlay1 right after the name, then the
// name colour again; an unpinned one is unchanged.
func TestBorderFormatPin(t *testing.T) {
	c := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { c.KillServer() })
	if _, err := c.Run("new-session", "-d", "-s", "f", "-n", "main", "cat"); err != nil {
		t.Skip("tmux unavailable")
	}
	var format string
	for _, a := range tmuxCommands(&fleet.Fleet{T: c, Exe: "/bin/true"}) {
		if len(a) == 4 && a[2] == "pane-border-format" {
			format = a[3]
		}
	}
	pane, _ := c.Run("display-message", "-p", "-t", "f:main", "#{pane_id}")
	for k, v := range map[string]string{"@fleet_agent": "a1", "@fleet_glyph": "●", "@fleet_status": "idle", "@fleet_color": "#f9e2af"} {
		c.SetPaneOption(pane, k, v)
	}
	plain, _ := c.Run("display-message", "-p", "-t", pane, format)
	if strings.Contains(plain, "⚑") {
		t.Fatalf("unpinned: %q", plain)
	}
	c.SetPaneOption(pane, "@fleet_pin", "1")
	got, _ := c.Run("display-message", "-p", "-t", pane, format)
	if want := "● a1 #[fg=" + theme.Overlay1 + "]⚑#[fg=#f9e2af] #[fg="; !strings.Contains(got, want) {
		t.Errorf("pinned: %q, want %q", got, want)
	}
}

func TestPinMsg(t *testing.T) {
	if pinMsg("a1", true) != "a1 pinned ⚑" || pinMsg("a1", false) != "a1 unpinned" {
		t.Error(pinMsg("a1", true), pinMsg("a1", false))
	}
}

// Colours that capture-pane -e carries from one line into the next survive
// the crop: no reset between lines, one at the very end, and the cut never
// splits an escape sequence.
func TestFitScreenKeepsColoursAcrossLines(t *testing.T) {
	in := "\x1b[31mred starts here\nstill red\nabcd\x1b[38;2;1;2;3mefgh\n"
	got := fitScreen(in, 4, 3)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("lines %q", got)
	}
	if strings.Contains(lines[0], "\x1b[0m") || strings.Contains(lines[1], "\x1b[0m") {
		t.Errorf("reset inside the screen drops the carried colour: %q", got)
	}
	if !strings.HasSuffix(got, "\x1b[0m") || strings.Count(got, "\x1b[0m") != 1 {
		t.Errorf("want one reset at the end: %q", got)
	}
	if !strings.HasPrefix(lines[0], "\x1b[31m") {
		t.Errorf("colour lost: %q", lines[0])
	}
	want := []string{"red ", "stil", "abcd"}
	for i, l := range lines {
		if s := ansi.Strip(l); s != want[i] {
			t.Errorf("line %d %q, want %q (escape sequence cut?)", i, s, want[i])
		}
	}
}

// --notify (the right-click menu) reports failures as a tmux message, so
// run-shell never opens view mode; without it the error is returned.
func TestWallNotifySwallowsErrors(t *testing.T) {
	f := testFleet(t)
	if err := cmdWall(f, []string{"pin", "nosuch"}); err == nil {
		t.Error("pin of an unknown agent must fail")
	}
	if err := cmdWall(f, []string{"pin", "--notify", "nosuch"}); err != nil {
		t.Errorf("--notify: %v", err)
	}
}
