package main

import (
	"errors"
	"os"
	"os/exec"
	"runtime"

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
	if getenv("WAYLAND_DISPLAY") != "" && has("wl-copy") {
		return "wl-copy"
	}
	if getenv("DISPLAY") != "" {
		if has("xclip") {
			return "xclip -selection clipboard -in"
		}
		if has("xsel") {
			return "xsel --clipboard --input"
		}
	}
	return ""
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
	switch {
	case errors.Is(err, companion.ErrOff):
		return ""
	case err == nil && t.Source != "PATH":
		return tools.ShellCommand(t.Argv())
	}
	return copyCommand(os.Getenv, exec.LookPath, runtime.GOOS)
}
