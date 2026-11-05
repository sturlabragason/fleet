package main

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/keys"
	"github.com/sturlabragason/fleet/internal/theme"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/tools"
)

func TestSupervisorMCPArgs(t *testing.T) {
	got := supervisorMCPArgs(tools.Profile{Patterns: "copilot"})
	want := []string{"--no-custom-instructions", "--allow-all-mcp-server-instructions", "--available-tools", "fleet", "--allow-tool", "fleet", "--disable-builtin-mcps"}
	if !reflect.DeepEqual(got[:len(want)], want) {
		t.Fatalf("copilot: %v", got)
	}
	if got := supervisorMCPArgs(tools.Profile{Patterns: "claude"}); got != nil {
		t.Fatalf("claude should get no copilot flags: %v", got)
	}
}

func TestDisableUserMCPArgs(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mcp-config.json")
	os.WriteFile(p, []byte(`{"mcpServers":{"zeta":{},"fleet":{},"alpha":{}}}`), 0o600)
	want := []string{"--disable-mcp-server", "alpha", "--disable-mcp-server", "zeta"}
	if got := disableUserMCPArgs(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if got := disableUserMCPArgs(filepath.Join(t.TempDir(), "missing.json")); got != nil {
		t.Fatalf("missing file: %v", got)
	}
}

// The hooks must include pane-focus-in (target slot + cached refresh) next to
// client-resized/client-attached.
func TestConfigureSetsPaneFocusHook(t *testing.T) {
	c := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { c.KillServer() })
	if _, err := c.Run("new-session", "-d", "-s", "f", "-n", "main", "cat"); err != nil {
		t.Skip("tmux unavailable")
	}
	configure(&fleet.Fleet{T: c, Exe: "/usr/bin/fleet"})
	// pane-focus-in is a window-scoped hook, so it only shows with -gw.
	// client-resized/client-attached force a real apply; pane-focus-in fires on
	// every pane switch, so it takes the cached path instead.
	for _, h := range []struct{ scope, name, want string }{
		{"-g", "client-resized", "wall apply"},
		{"-g", "client-attached", "wall apply"},
		{"-gw", "pane-focus-in", "wall refresh"},
	} {
		hooks, err := c.Run("show-hooks", h.scope)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, line := range strings.Split(hooks, "\n") {
			if strings.HasPrefix(line, h.name+"[") {
				found = true
				if !strings.Contains(line, h.want) {
					t.Errorf("%s must run `%s`, got %q", h.name, h.want, line)
				}
			}
		}
		if !found {
			t.Errorf("hook %s is not set", h.name)
		}
	}
}

// DESIGN.md §5e: the pane-focus-in hook makes a focused wall pane the target
// slot, inline, and focusing the overview, the admiral or anything else
// leaves it alone. Runs the configured hook on a real tmux server.
func TestFocusHookSetsTargetForWallPanesOnly(t *testing.T) {
	c := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { c.KillServer() })
	ov, err := c.Run("new-session", "-d", "-s", "f", "-n", "main", "-x", "200", "-y", "50", "-P", "-F", "#{pane_id}", "cat")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	sup, _ := c.Run("split-window", "-d", "-h", "-t", "f:main", "-P", "-F", "#{pane_id}", "cat")
	w1, _ := c.Run("split-window", "-d", "-h", "-t", "f:main", "-P", "-F", "#{pane_id}", "cat")
	w2, _ := c.Run("split-window", "-d", "-h", "-t", "f:main", "-P", "-F", "#{pane_id}", "cat")
	c.SetPaneOption(ov, "@role", "ov")
	c.SetPaneOption(sup, "@role", "sup")
	c.SetPaneOption(w1, "@role", "wall")
	c.SetPaneOption(w2, "@role", "wall")
	// /bin/true: the hook's `wall refresh` must not run a real fleet here.
	configure(&fleet.Fleet{T: c, Exe: "/bin/true"})
	target := func() string {
		out, _ := c.Run("show-options", "-wqv", "-t", "f:main", "@fleet_target")
		return strings.TrimSpace(out)
	}
	// A detached server fires no focus events, so run the hook as tmux would.
	focus := func(p string) {
		t.Helper()
		if _, err := c.Run("set-hook", "-R", "-t", p, "pane-focus-in"); err != nil {
			t.Fatal(err)
		}
	}
	focus(w1)
	if got := target(); got != w1 {
		t.Fatalf("focus wall pane %s: target %q", w1, got)
	}
	focus(ov)
	focus(sup)
	if got := target(); got != w1 {
		t.Errorf("focusing the overview/admiral changed the target to %q", got)
	}
	focus(w2)
	if got := target(); got != w2 {
		t.Errorf("focus wall pane %s: target %q", w2, got)
	}
}

// The target pane's title starts with a bold ▶ in the select colour and the
// "next out" pane's title ends with a dim mark; other panes are unchanged.
func TestBorderFormatTargetAndNextOut(t *testing.T) {
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
	for k, v := range map[string]string{"@fleet_agent": "a1", "@fleet_glyph": "●", "@fleet_status": "waiting", "@fleet_color": "#f9e2af"} {
		c.SetPaneOption(pane, k, v)
	}
	expand := func() string {
		out, err := c.Run("display-message", "-p", "-t", pane, format)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	plain := expand()
	if strings.Contains(plain, "▶") || strings.Contains(plain, "next out") {
		t.Fatalf("no target, no next out: %q", plain)
	}
	c.Run("set-option", "-w", "-t", "f:main", "@fleet_target", pane)
	got := expand()
	if want := "#[fg=" + theme.Select + "]#[bold]▶#[nobold] #[fg=#f9e2af]● a1"; !strings.Contains(got, want) {
		t.Errorf("target: %q, want it to contain %q", got, want)
	}
	c.Run("set-option", "-w", "-t", "f:main", "@fleet_target", "")
	c.Run("set-option", "-w", "-t", "f:main", "@fleet_next_out", pane)
	got = expand()
	if !strings.HasSuffix(strings.TrimSpace(got), "waiting#[default] #[fg="+theme.Gone+"]· next out#[default]") {
		t.Errorf("next out: %q", got)
	}
}

// Every fleet pane runs a program that turns mouse reporting on (Copilot,
// Claude, the overview TUI), so tmux's default drag binding handed the drag to
// the program and copy-mode never started: the drag selected nothing, ADR-28's
// copy-command never ran, and with `mouse on` the terminal made no native
// selection either, so the clipboard stayed empty (ADR-38). The bindings must
// not test mouse_any_flag, and tmux must accept them.
func TestMouseDragSelectsOverAppMouse(t *testing.T) {
	binds := map[string]string{}
	for _, c := range tmuxCommands(&fleet.Fleet{Exe: "/bin/true"}) {
		if len(c) == 4 && c[0] == "bind-key" && c[1] == "-n" {
			binds[c[2]] = c[3]
		}
	}
	for _, k := range mouseSelectKeys {
		v, ok := binds[k]
		if !ok {
			t.Fatalf("%s is not bound", k)
		}
		if strings.Contains(v, "mouse_any_flag") {
			t.Errorf("%s still gives the drag to the pane's program: %q", k, v)
		}
		if !strings.Contains(v, "copy-mode") {
			t.Errorf("%s must start a tmux selection: %q", k, v)
		}
		// The overview acts on the left-button release (ADR-31), which tmux
		// swallows once it takes the drag, so it keeps the old behaviour.
		if !strings.Contains(v, "#{==:#{@role},ov}") {
			t.Errorf("%s must leave the overview's clicks alone: %q", k, v)
		}
	}
	if !strings.Contains(binds["DoubleClick1Pane"], "copy-pipe-and-cancel") ||
		!strings.Contains(binds["TripleClick1Pane"], "copy-pipe-and-cancel") {
		t.Error("double and triple click must pipe the word or line to copy-command")
	}

	c := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { c.KillServer() })
	if _, err := c.Run("new-session", "-d", "-s", "f", "cat"); err != nil {
		t.Skip("tmux unavailable")
	}
	for _, k := range mouseSelectKeys {
		v := binds[k]
		if _, err := c.Run("bind-key", "-n", k, v); err != nil {
			t.Fatalf("tmux rejected the %s binding %q: %v", k, v, err)
		}
		got, err := c.Run("list-keys", "-T", "root")
		if err != nil {
			t.Fatal(err)
		}
		var line string
		for _, l := range strings.Split(got, "\n") {
			if strings.Contains(l, " "+k+" ") {
				line = l
			}
		}
		if !strings.Contains(line, "copy-mode") {
			t.Errorf("%s round-tripped as %q", k, line)
		}
	}
}

// fleet up unbinds the drags that move panes out of the wall order.
func TestUnbindsPaneDrags(t *testing.T) {
	want := map[string]bool{"M-MouseDrag1Pane": true, "M-MouseDrag1Border": true, "C-MouseDrag1Pane": true}
	for _, c := range tmuxCommands(&fleet.Fleet{Exe: "/bin/true"}) {
		if len(c) == 3 && c[0] == "unbind-key" && c[1] == "-n" {
			delete(want, c[2])
		}
	}
	if len(want) != 0 {
		t.Errorf("not unbound: %v", want)
	}
}

// Outside the fleet server there is no wall, so the refresh-driven apply must
// be a silent no-op rather than an error (and a log line) every second.
func TestApplyWallDisabledIsANoop(t *testing.T) {
	b := &backend{enabled: false} // no wall client at all: must not be touched
	if err := b.ApplyWall(); err != nil {
		t.Errorf("disabled wall returned %v", err)
	}
}

// TestTmuxBindingsMatchRegistry: every root-table Alt key and every wall
// chord key fleet up binds is in the keys registry, and vice versa.
func TestTmuxBindingsMatchRegistry(t *testing.T) {
	bound, chord := map[string]bool{}, map[string]bool{}
	var statusRight, popup []string
	for _, c := range tmuxCommands(&fleet.Fleet{Exe: "/opt/my fleet/fleet"}) {
		if len(c) > 3 && c[0] == "bind-key" && c[1] == "-n" && strings.HasPrefix(c[2], "M-") {
			bound[c[2]] = true
			if c[2] == keys.HelpTmuxKey {
				popup = c
			}
		}
		if len(c) > 4 && c[0] == "bind-key" && c[1] == "-T" && c[2] == keys.ChordTable {
			chord[c[3]] = true
		}
		if len(c) == 4 && c[0] == "set-option" && c[2] == "status-right" {
			statusRight = c
		}
	}
	reg := keys.TmuxKeys()
	for k := range bound {
		if !reg[k] {
			t.Errorf("tmux binds %s but the keys registry doesn't list it", k)
		}
	}
	for k := range reg {
		if !bound[k] {
			t.Errorf("keys registry lists %s but fleet up doesn't bind it", k)
		}
	}
	regChord := keys.ChordKeys()
	if !reflect.DeepEqual(chord, regChord) {
		t.Errorf("-T %s binds %v, keys registry claims %v", keys.ChordTable, chord, regChord)
	}
	if statusRight == nil || popup == nil {
		t.Fatal("tmuxCommands must set status-right and bind the help popup")
	}
	if !strings.Contains(statusRight[3], "Alt-? all keys") || strings.Contains(statusRight[3], "M-") {
		t.Errorf("status-right %q", statusRight[3])
	}
	if got := popup[len(popup)-1]; got != "'/opt/my fleet/fleet' keys --popup" {
		t.Errorf("popup command %q", got)
	}
}

// The wall border shows a ◆ in the team colour after the agent name, and
// expands exactly as before for a pane without a team.
func TestBorderFormatTeamMark(t *testing.T) {
	c := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { c.KillServer() })
	if _, err := c.Run("new-session", "-d", "-s", "f", "-n", "main", "cat"); err != nil {
		t.Skip("tmux unavailable")
	}
	var format string
	for _, a := range tmuxCommands(&fleet.Fleet{T: c, Exe: "/usr/bin/fleet"}) {
		if len(a) == 4 && a[2] == "pane-border-format" {
			format = a[3]
		}
	}
	old := strings.Replace(format, "#{?@fleet_team, #[fg=#{@fleet_team_color}]◆,}", "", 1)
	if format == "" || old == format {
		t.Fatalf("format has no team mark: %q", format)
	}
	pane, _ := c.Run("display-message", "-p", "-t", "f:main", "#{pane_id}")
	for k, v := range map[string]string{"@fleet_agent": "a1", "@fleet_glyph": "●", "@fleet_status": "waiting", "@fleet_color": "#f9e2af"} {
		c.SetPaneOption(pane, k, v)
	}
	expand := func(f string) string {
		out, err := c.Run("display-message", "-p", "-t", pane, f)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got, want := expand(format), expand(old); got != want {
		t.Errorf("no team: got %q, want today's %q", got, want)
	}
	c.SetPaneOption(pane, "@fleet_team", "alpha")
	c.SetPaneOption(pane, "@fleet_team_color", "#89b4fa")
	got := expand(format)
	if !strings.Contains(got, "● a1 #[fg=#89b4fa]◆ #[fg=") || strings.Contains(got, "alpha") {
		t.Errorf("team: %q", got)
	}
}

// VTE terminals (Ptyxis, GNOME Terminal) drop tmux's OSC 52, so a copy in a
// fleet pane never reached the system clipboard. copy-command must name the
// session's clipboard tool.
func TestCopyCommand(t *testing.T) {
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			for i := 0; i+1 < len(kv); i += 2 {
				if kv[i] == k {
					return kv[i+1]
				}
			}
			return ""
		}
	}
	look := func(have ...string) func(string) (string, error) {
		return func(b string) (string, error) {
			for _, h := range have {
				if h == b {
					return "/usr/bin/" + b, nil
				}
			}
			return "", exec.ErrNotFound
		}
	}
	for _, c := range []struct {
		name string
		env  func(string) string
		look func(string) (string, error)
		goos string
		want string
	}{
		{"wayland", env("WAYLAND_DISPLAY", "wayland-0", "DISPLAY", ":0"), look("wl-copy", "xclip"), "linux", "wl-copy"},
		{"wayland without wl-copy falls back to X", env("WAYLAND_DISPLAY", "wayland-0", "DISPLAY", ":0"), look("xclip"), "linux", "xclip -selection clipboard -in"},
		{"x11 xsel", env("DISPLAY", ":0"), look("xsel", "wl-copy"), "linux", "xsel --clipboard --input"},
		{"macos", env(), look("pbcopy"), "darwin", "pbcopy"},
		{"headless", env(), look("wl-copy", "xclip"), "linux", ""},
	} {
		if got := copyCommand(c.env, c.look, c.goos); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Lavender (theme.Select) means "the ▶ target slot" and nothing else, so the
// active pane border and the pane-number overlay must not use it; the active
// pane is told apart by brightness instead (ADR-37).
func TestSelectColourIsTargetOnly(t *testing.T) {
	opts := map[string]string{}
	for _, c := range tmuxCommands(&fleet.Fleet{Exe: "/bin/true"}) {
		if len(c) == 4 && c[0] == "set-option" && c[1] == "-g" {
			opts[c[2]] = c[3]
		}
	}
	for _, o := range []string{"pane-active-border-style", "display-panes-active-colour"} {
		if v, ok := opts[o]; !ok || strings.Contains(v, theme.Select) {
			t.Errorf("%s = %q, must not use the select colour", o, v)
		}
	}
	if !strings.Contains(opts["pane-active-border-style"], theme.Text) {
		t.Errorf("the active border must be the bright neutral: %q", opts["pane-active-border-style"])
	}
	// Unfocused panes recede, but stay a step above the resting Overlay1 so an
	// emphasised pane you are not in is still readable.
	if want := "fg=" + theme.Overlay2 + ",bg=" + theme.Base; opts["window-style"] != want {
		t.Errorf("window-style = %q, want %q", opts["window-style"], want)
	}
	if want := "fg=" + theme.Text + ",bg=" + theme.Base; opts["window-active-style"] != want {
		t.Errorf("window-active-style = %q, want %q", opts["window-active-style"], want)
	}
	if !strings.Contains(targetMark, theme.Select) {
		t.Error("the ▶ target mark must keep the select colour")
	}
}
