package main

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/tmux"
)

func TestTerminalArgv(t *testing.T) {
	cmd := []string{"/bin/fleet", "wall", "attach"}
	has := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			for _, h := range names {
				if h == n {
					return "/usr/bin/" + n, nil
				}
			}
			return "", errors.New("not found")
		}
	}
	for _, c := range []struct {
		override, term string
		look           func(string) (string, error)
		want           []string
	}{
		{"foot --app-id fleet", "kitty", has("ptyxis"), []string{"foot", "--app-id", "fleet", "/bin/fleet", "wall", "attach"}},
		{"", "/usr/bin/alacritty", has(), []string{"/usr/bin/alacritty", "-e", "/bin/fleet", "wall", "attach"}},
		{"", "kitty", has(), []string{"kitty", "/bin/fleet", "wall", "attach"}},
		{"", "myterm", has(), []string{"myterm", "-e", "/bin/fleet", "wall", "attach"}},
		{"", "", has("xterm", "ptyxis"), []string{"/usr/bin/ptyxis", "--", "/bin/fleet", "wall", "attach"}},
		{"", "", has("gnome-terminal"), []string{"/usr/bin/gnome-terminal", "--window", "--", "/bin/fleet", "wall", "attach"}},
		{"", "", has(), nil},
	} {
		got, ok := terminalArgv(c.override, c.term, c.look, cmd)
		if ok != (c.want != nil) || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q %q: got %v %v, want %v", c.override, c.term, got, ok, c.want)
		}
	}
}

// Two monitors and back on a real tmux server: the admirals leave the main
// window for the admiral window and come back, the wall session closes, and
// no agent pane is touched.
func TestSplitAndJoinMonitors(t *testing.T) {
	c := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { c.KillServer() })
	sup, err := c.Run("new-session", "-d", "-s", fleet.Session, "-n", "main", "-x", "300", "-y", "80", "-P", "-F", "#{pane_id}", "cat")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	c.SetPaneOption(sup, "@role", "sup")
	sup2, _ := c.Run("split-window", "-d", "-v", "-t", sup, "-P", "-F", "#{pane_id}", "cat")
	c.SetPaneOption(sup2, "@role", "sup")
	c.SetPaneOption(sup2, "@fleet_admiral", "print")
	ov, _ := c.Run("split-window", "-d", "-h", "-t", sup, "-P", "-F", "#{pane_id}", "cat")
	c.SetPaneOption(ov, "@role", "ov")
	ag, _ := c.Run("split-window", "-d", "-v", "-t", ov, "-P", "-F", "#{pane_id}", "cat")
	c.SetPaneOption(ag, "@role", "wall")
	c.SetPaneOption(ag, "@fleet_agent", "a1")
	f := &fleet.Fleet{T: c}

	roles := func(target string) map[string]string {
		ps, _ := c.ListPanes(target)
		out := map[string]string{}
		for _, p := range ps {
			out[p.ID] = p.Role
		}
		return out
	}
	if err := splitMonitors(f); err != nil {
		t.Fatal(err)
	}
	if got := roles(fleet.Session + ":main"); len(got) != 2 || got[ov] != "ov" || got[ag] != "wall" {
		t.Errorf("main after split: %v", got)
	}
	if got := roles(fleet.Session + ":" + admiralWindow); len(got) != 2 || got[sup] != "sup" || got[sup2] != "sup" {
		t.Errorf("admiral window: %v", got)
	}
	if !twoMonitors(f) || admiralTarget(f) == "" {
		t.Errorf("two monitors %v, admiral target %q", twoMonitors(f), admiralTarget(f))
	}

	if _, err := c.Run("new-session", "-d", "-t", "="+fleet.Session, "-s", wallSession); err != nil {
		t.Fatal(err)
	}
	if err := joinMonitors(f); err != nil {
		t.Fatal(err)
	}
	if got := roles(fleet.Session + ":main"); len(got) != 4 {
		t.Errorf("main after join: %v", got)
	}
	if c.HasSession(wallSession) || twoMonitors(f) || admiralTarget(f) != "" {
		t.Errorf("after join: wall session %v, two monitors %v", c.HasSession(wallSession), twoMonitors(f))
	}
}
