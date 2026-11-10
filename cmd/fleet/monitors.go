package main

// Two-monitor mode (DESIGN §5a, ADR-108): the admiral gets a window of its
// own on monitor 1, and a second tmux session grouped with the fleet
// session shows the main window (overview + wall) on monitor 2. A grouped
// session shares every window, so both terminals drive the same agents;
// each client picks its own current window, and each window is sized by
// the one client looking at it.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/tools"
	"github.com/sturlabragason/fleet/internal/wall"
)

const (
	// wallSession is the session monitor 2 attaches to. Its name extends the
	// fleet session's, so `list-panes -a` lists the fleet session's rows first
	// and the duplicate rows of the shared windows are the ones dropped.
	wallSession = fleet.Session + "-wall"
	// admiralWindow holds the admirals while they are off the main window.
	admiralWindow = "admiral"
	// monitorsOpt (global) is "2" while two-monitor mode is on, so a plain
	// `fleet up` attaches to the admiral's window, not to the wall's.
	monitorsOpt = "@fleet_monitors"
)

// twoMonitors reports whether two-monitor mode is on.
func twoMonitors(f *fleet.Fleet) bool {
	v, _ := f.T.Run("show-options", "-gqv", monitorsOpt)
	return strings.TrimSpace(v) == "2"
}

// splitMonitors moves every admiral in the main window into the admiral
// window (main first, the others stacked under it) and lays the wall out
// without them. Admirals already elsewhere (Alt-s) stay where they are.
func splitMonitors(f *fleet.Fleet) error {
	panes, err := f.T.ListPanes(fleet.Session + ":main")
	if err != nil {
		return err
	}
	var sups []tmux.PaneInfo
	for _, p := range panes {
		if p.Role != "sup" {
			continue
		}
		if p.Admiral == "" || p.Admiral == "main" {
			sups = append([]tmux.PaneInfo{p}, sups...)
		} else {
			sups = append(sups, p)
		}
	}
	if len(sups) > 0 {
		first := sups[0].ID
		if _, err := f.T.Run("break-pane", "-d", "-s", first, "-t", fleet.Session+":", "-n", admiralWindow); err != nil {
			return err
		}
		for _, p := range sups[1:] {
			if _, err := f.T.Run("join-pane", "-d", "-v", "-s", p.ID, "-t", first); err != nil {
				return err
			}
		}
	}
	f.T.Run("set-option", "-g", monitorsOpt, "2")
	logx.For("monitors").Info("two monitors", "moved", len(sups))
	return wall.New(f.T, fleet.Session).Apply()
}

// joinMonitors undoes splitMonitors: the admirals come back into the main
// window and the wall session is closed (its windows are the fleet
// session's, so no agent goes with it).
func joinMonitors(f *fleet.Fleet) error {
	f.T.Run("set-option", "-gu", monitorsOpt)
	if f.T.HasSession(wallSession) {
		f.T.Run("kill-session", "-t", "="+wallSession)
	}
	w := wall.New(f.T, fleet.Session)
	s, err := w.Read()
	if err != nil {
		return err
	}
	if len(s.Sups) > 0 {
		return w.Apply()
	}
	return w.ToggleSup()
}

// admiralTarget is the window holding the main admiral when it is not in
// the main window, "" when it is or there is none.
func admiralTarget(f *fleet.Fleet) string {
	panes, _ := f.T.ListPanes("")
	for _, p := range panes {
		if p.Session == fleet.Session && p.Role == "sup" && (p.Admiral == "" || p.Admiral == "main") && p.Window != "main" {
			return p.WindowID
		}
	}
	return ""
}

// attachTmux replaces fleet with `tmux attach`-like args on the fleet socket.
func attachTmux(args ...string) error {
	tm, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	env := []string{}
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "TMUX=") {
			env = append(env, e)
		}
	}
	return syscall.Exec(tm, append([]string{"tmux", "-L", fleet.Socket}, args...), env)
}

// wallAttach is `fleet wall attach`: this terminal shows the main window
// (overview + wall) through the wall session, whatever the other terminal
// shows. The session goes away when its terminal closes.
func wallAttach(f *fleet.Fleet) error {
	if !f.T.HasSession(fleet.Session) {
		return fmt.Errorf("fleet is not up; run fleet up --monitors 2")
	}
	if f.T.HasSession(wallSession) {
		f.T.Run("select-window", "-t", wallSession+":main")
		return attachTmux("attach-session", "-t", "="+wallSession)
	}
	return attachTmux(wallSessionArgs()...)
}

// wallSessionArgs creates and attaches the wall session in one tmux call.
func wallSessionArgs() []string {
	return []string{
		"new-session", "-t", "=" + fleet.Session, "-s", wallSession, ";",
		"set-option", "-t", wallSession, "destroy-unattached", "on", ";",
		"select-window", "-t", wallSession + ":main",
	}
}

// knownTerminals are emulators fleet can open a window in, in the order it
// looks for them, with the args that run a command in a new window.
var knownTerminals = []struct {
	name string
	args []string
}{
	{"ptyxis", []string{"--"}},
	{"gnome-terminal", []string{"--window", "--"}},
	{"kitty", nil},
	{"foot", nil},
	{"alacritty", []string{"-e"}},
	{"wezterm", []string{"start", "--"}},
	{"ghostty", []string{"-e"}},
	{"konsole", []string{"-e"}},
	{"xterm", []string{"-e"}},
}

// terminalArgv is the command that opens a terminal window running cmd:
// FLEET_TERMINAL (its words, then cmd), else $TERMINAL (a known emulator's
// args, or -e), else the first known emulator on PATH. ok is false when
// none is found.
func terminalArgv(override, termEnv string, look func(string) (string, error), cmd []string) ([]string, bool) {
	if w := strings.Fields(override); len(w) > 0 {
		return append(w, cmd...), true
	}
	if w := strings.Fields(termEnv); len(w) > 0 {
		for _, t := range knownTerminals {
			if filepath.Base(w[0]) == t.name {
				return append(append(w, t.args...), cmd...), true
			}
		}
		return append(append(w, "-e"), cmd...), true
	}
	for _, t := range knownTerminals {
		if path, err := look(t.name); err == nil {
			return append(append([]string{path}, t.args...), cmd...), true
		}
	}
	return nil, false
}

// openWallTerminal opens a new terminal window running `fleet wall attach`.
// It returns the command it ran, or an error saying what to run by hand.
func openWallTerminal(f *fleet.Fleet) (string, error) {
	argv, ok := terminalArgv(os.Getenv("FLEET_TERMINAL"), os.Getenv("TERMINAL"), exec.LookPath, []string{f.Exe, "wall", "attach"})
	if !ok {
		return "", fmt.Errorf("no terminal found (set FLEET_TERMINAL)")
	}
	c := exec.Command(argv[0], argv[1:]...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		return "", err
	}
	c.Process.Release()
	line := tools.ShellCommand(argv)
	logx.For("monitors").Info("opened wall terminal", "argv", line)
	return line, nil
}
