package main

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/sturlabragason/fleet/internal/companion"
	"github.com/sturlabragason/fleet/internal/tools"
)

// copyCommand picks the program tmux's copy-pipe sends a copy-mode selection
// to, so a mouse or keyboard copy in a fleet pane reaches the system
// clipboard. tmux's own OSC 52 path stays on, but VTE terminals (GNOME
// Terminal, Ptyxis) ignore OSC 52. Empty means no known tool: leave tmux's
// default. See DESIGN.md ADR-28.
func copyCommand(getenv func(string) string, lookPath func(string) (string, error), goos string) string {
	has := func(bin string) bool { _, err := lookPath(bin); return err == nil }
	if goos == "darwin" && has("pbcopy") {
		return "pbcopy"
	}
	const xclip = "xclip -selection clipboard -in"
	if getenv("WAYLAND_DISPLAY") != "" && has("wl-copy") {
		// Mutter has no data-control protocol, so wl-copy maps a window to get
		// focus (it shows as "wl-clipboard" in Alt-Tab). xclip through
		// Xwayland reaches the same clipboard with no window (ADR-28).
		if isGNOME(getenv("XDG_CURRENT_DESKTOP")) && getenv("DISPLAY") != "" && has("xclip") {
			return xclip
		}
		return "wl-copy"
	}
	if getenv("DISPLAY") != "" {
		if has("xclip") {
			return xclip
		}
		if has("xsel") {
			return "xsel --clipboard --input"
		}
	}
	return ""
}

func isGNOME(desktop string) bool {
	for _, d := range strings.Split(desktop, ":") {
		if strings.EqualFold(d, "GNOME") {
			return true
		}
	}
	return false
}

// guardedCopyCommand wraps cmd so tmux's copy-command never hands it an empty
// or whitespace-only selection: a click into a pane runs copy-command with
// nothing selected, and passing that on wiped the clipboard. It runs under sh
// explicitly because tmux uses default-shell, which may be fish. The selection
// is held in a variable (the x keeps trailing newlines), so no temp file is
// left behind. wl-copy runs in the foreground with its PID in a file, and the
// next copy stops the previous one, so a wl-copy that never got focus does
// not linger (ADR-28).
func guardedCopyCommand(cmd string) string {
	script := `s=$(cat; echo x); s=${s%x}
case $s in *[![:space:]]*) ;; *) exit 0;; esac
`
	if cmd == "wl-copy" {
		script += `p=${XDG_RUNTIME_DIR:-${TMPDIR:-/tmp}}/fleet-wl-copy.pid
o=$(cat "$p" 2>/dev/null)
printf %s "$s" | wl-copy --foreground >/dev/null 2>&1 &
echo $! >"$p"
case $o in ''|*[!0-9]*) exit 0;; esac
case $(tr '\0' ' ' <"/proc/$o/cmdline" 2>/dev/null) in *'wl-copy --foreground'*) kill "$o" 2>/dev/null;; esac
exit 0`
	} else {
		script += `printf %s "$s" | ` + cmd + ` >/dev/null 2>&1`
	}
	return "sh -c " + tools.Quote(script)
}

// clipboardSpec is the clipboard as a companion (ADR-126): [integrations.clipboard]
// path = "off" stops fleet setting copy-pipe, a path makes it pipe there.
var clipboardSpec = companion.Register(companion.Spec{
	Name:    "clipboard",
	Bins:    []string{"pbcopy", "wl-copy", "xclip", "xsel"},
	Feature: "copying a tmux selection to the system clipboard",
	Hint:    "install wl-clipboard (Wayland) or xclip (X11)",
})

func hostCopyCommand() string {
	t, err := companion.Find(clipboardSpec)
	cmd := ""
	switch {
	case errors.Is(err, companion.ErrOff):
		return ""
	case err == nil && t.Source != "PATH":
		cmd = tools.ShellCommand(t.Argv())
	default:
		cmd = copyCommand(os.Getenv, exec.LookPath, runtime.GOOS)
	}
	if cmd == "" {
		return ""
	}
	return guardedCopyCommand(cmd)
}
