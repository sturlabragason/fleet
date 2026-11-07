package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/mcp"
	"github.com/sturlabragason/fleet/internal/supervisor"
)

func TestResolveAdmiralName(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == AdmiralEnv {
				return v
			}
			return ""
		}
	}
	pane := func(v string) func() string { return func() string { return v } }
	cases := []struct {
		flag, env, pane, want string
	}{
		{"a", "b", "c", "a"},
		{"", "b", "c", "b"},
		{"", "", "c", "c"},
		{"", "", "", ""},
	}
	for _, c := range cases {
		if got := resolveAdmiralName(c.flag, env(c.env), pane(c.pane)); got != c.want {
			t.Errorf("%+v: got %q", c, got)
		}
	}
	if got := resolveAdmiralName("", env(""), nil); got != "" {
		t.Errorf("nil pane lookup: %q", got)
	}
}

func TestCheckAdmiral(t *testing.T) {
	home := t.TempDir()
	if _, err := supervisor.Add(home, "print", "", 3); err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{"main": "main", "print": "print", "": mcp.Unknown} {
		if got, err := checkAdmiral(home, in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	_, err := checkAdmiral(home, "x")
	if err == nil || !strings.Contains(err.Error(), `unknown admiral "x" (fleet admiral ls)`) {
		t.Errorf("unknown: %v", err)
	}
	// A torn registry degrades to unknown identity instead of exiting.
	os.WriteFile(filepath.Join(home, "admirals", "registry.json"), []byte("{torn"), 0o600)
	if got, err := checkAdmiral(home, "print"); err != nil || got != mcp.Unknown {
		t.Errorf("torn registry: %q %v", got, err)
	}
}

func TestPaneAdmiralOf(t *testing.T) {
	sock := func() string { return "/tmp/s" }
	opts := func(m map[string]string) func(p, k string) string {
		return func(p, k string) string { return m[p+" "+k] }
	}
	sup := opts(map[string]string{"%1 @role": "sup"})
	tagged := opts(map[string]string{"%1 @role": "sup", "%1 @fleet_admiral": "print"})
	cases := []struct {
		name, pane, env string
		opt             func(p, k string) string
		want            string
	}{
		{"untagged sup pane is main", "%1", "/tmp/s,1,0", sup, "main"},
		{"tagged", "%1", "/tmp/s,1,0", tagged, "print"},
		{"pane not found", "%9", "/tmp/s,1,0", sup, ""},
		{"not an admiral pane", "%1", "/tmp/s,1,0", opts(map[string]string{"%1 @role": "agent"}), ""},
		{"no TMUX_PANE", "", "/tmp/s,1,0", sup, ""},
		{"other socket", "%1", "/tmp/other,1,0", sup, ""},
	}
	for _, c := range cases {
		if got := paneAdmiralOf(c.pane, c.env, sock, c.opt); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
