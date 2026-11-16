package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/fleet"
)

// runWith points run at f instead of the live fleet socket.
func runWith(t *testing.T, f *fleet.Fleet) {
	t.Helper()
	t.Setenv("TMUX_PANE", "")
	old := openFleet
	openFleet = func() (*fleet.Fleet, error) {
		if f == nil {
			return nil, errors.New("fleet opened")
		}
		return f, nil
	}
	t.Cleanup(func() { openFleet = old })
}

// Commands that need no fleet state never open it, and an extension command
// is found before the fleet is opened.
func TestRunStatelessDoesNotOpenFleet(t *testing.T) {
	isolate(t)
	runWith(t, nil)
	if out := captureOut(t, func() error { return run([]string{"version"}) }); !strings.Contains(out, "fleet") {
		t.Errorf("version printed %q", out)
	}
	if out := captureOut(t, func() error { return run([]string{"playbook", "--effort", "light"}) }); out == "" {
		t.Error("playbook printed nothing")
	}
	old := extCmds
	extCmds = map[string]func([]string) error{"zz-ext": func(a []string) error {
		if len(a) != 1 || a[0] != "x" {
			t.Errorf("ext got %q", a)
		}
		return errors.New("ext ran")
	}}
	t.Cleanup(func() { extCmds = old })
	if err := run([]string{"zz-ext", "x"}); err == nil || err.Error() != "ext ran" {
		t.Errorf("ext command: %v", err)
	}
	if err := run([]string{"ls"}); err == nil || err.Error() != "fleet opened" {
		t.Errorf("ls must open the fleet: %v", err)
	}
	if err := run(nil); err == nil || err.Error() != usage {
		t.Errorf("no args: %v", err)
	}
}

// The commands run handles inline: their usage errors, their output, and
// the error for a name nobody knows.
func TestRunDispatch(t *testing.T) {
	f := testFleet(t)
	t.Setenv("FLEET_HOME", f.Home)
	runWith(t, f)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"info"}, "usage: fleet info <name>"},
		{[]string{"info", "a", "b"}, "usage: fleet info <name>"},
		{[]string{"send"}, "usage: fleet send [--force] [--resume] <name> \"text\""},
		{[]string{"send", "--force", "--resume", "a"}, "usage: fleet send [--force] [--resume] <name> \"text\""},
		{[]string{"close"}, "usage: fleet close <name> [--squad]"},
		{[]string{"kill", "a", "b"}, "usage: fleet close <name> [--squad]"},
		{[]string{"close", "--squad"}, "usage: fleet close <name> [--squad]"},
		{[]string{"park"}, "usage: fleet park <name>"},
		{[]string{"wake", "--force"}, "usage: fleet wake <name> [--force]"},
		{[]string{"wake", "a", "b", "--force"}, "usage: fleet wake <name> [--force]"},
		{[]string{"nope"}, "unknown command \"nope\"\n" + usage},
	} {
		if err := run(c.args); err == nil || err.Error() != c.want {
			t.Errorf("run %q: %v, want %q", c.args, err, c.want)
		}
	}
	for _, args := range [][]string{{"info", "ghost"}, {"send", "ghost", "hi"}, {"park", "ghost"}, {"wake", "ghost"}, {"close", "ghost"}} {
		if err := run(args); err == nil || strings.HasPrefix(err.Error(), "usage:") {
			t.Errorf("run %q: %v, want the fleet's error", args, err)
		}
	}
	if out := captureOut(t, func() error { return run([]string{"help"}) }); strings.TrimSpace(out) != strings.TrimSpace(usage) {
		t.Errorf("help printed %q", out)
	}
	out := captureOut(t, func() error { return run([]string{"profiles"}) })
	if !strings.Contains(out, "fake ") || !strings.Contains(out, "\nprofiles: built-in + ") {
		t.Errorf("profiles printed %q", out)
	}
	var buf bytes.Buffer
	old := stderr
	stderr = &buf
	t.Cleanup(func() { stderr = old })
	captureOut(t, func() error { return run([]string{"supervisor", "ls"}) })
	if !strings.Contains(buf.String(), `"supervisor" is deprecated`) {
		t.Errorf("supervisor alias: stderr %q", buf.String())
	}
}
