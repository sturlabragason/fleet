package supervisor

import (
	"fmt"
	"strings"

	"github.com/sturlabragason/fleet/internal/tmux"
)

// ErrNoPane is returned when the main window has no @role=sup pane.
var ErrNoPane = fmt.Errorf("no admiral pane; run fleet up")

// FindPane returns the @role=sup pane in target (e.g. "fleet:main").
func FindPane(t *tmux.Client, target string) (string, error) { return FindPaneOf(t, target, Main) }

// FindPaneOf returns the admiral pane of one admiral: the @role=sup pane
// tagged @fleet_admiral=<name>; no tag means main (DESIGN §6.12).
func FindPaneOf(t *tmux.Client, target, name string) (string, error) {
	match := func(p tmux.PaneInfo) bool {
		return p.Role == "sup" && (isMain(name) && isMain(p.Admiral) || p.Admiral == name)
	}
	if ps, err := t.ListPanes(target); err == nil {
		for _, p := range ps {
			if match(p) {
				return p.ID, nil
			}
		}
	}
	// A hidden admiral lives in the sup-hidden window (Alt-s); search the
	// rest of the target's session, and only that session.
	sess, _, _ := strings.Cut(target, ":")
	if sess == "" {
		return "", ErrNoPane
	}
	ps, err := t.ListPanes("")
	if err != nil {
		return "", ErrNoPane
	}
	for _, p := range ps {
		if p.Session == sess && match(p) {
			return p.ID, nil
		}
	}
	return "", ErrNoPane
}

// Current returns session.json, or a record with only the session ID taken
// from the admiral pane's start command when session.json is missing.
func Current(home string, t *tmux.Client, target string) Session {
	return CurrentOf(home, t, target, Main)
}

// CurrentOf is Current for one admiral.
func CurrentOf(home string, t *tmux.Client, target, name string) Session {
	if s, ok := LoadOf(home, name); ok {
		return s
	}
	pane, err := FindPaneOf(t, target, name)
	if err != nil {
		return Session{}
	}
	cmd, _ := t.Run("display-message", "-p", "-t", pane, "#{pane_start_command}")
	return Session{SessionID: SessionIDFromCommand(cmd), PaneID: pane}
}

// CurrentFill is Fill for the current admiral.
func CurrentFill(home string, t *tmux.Client, target string) (used, max int, ok bool) {
	return CurrentFillOf(home, t, target, Main)
}

// CurrentFillOf is CurrentFill for one admiral.
func CurrentFillOf(home string, t *tmux.Client, target, name string) (used, max int, ok bool) {
	return Fill(CopilotHome(), CurrentOf(home, t, target, name).SessionID)
}
