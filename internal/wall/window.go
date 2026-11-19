package wall

import (
	"fmt"
	"strings"

	"github.com/sturlabragason/fleet/internal/logx"
)

// heal finds the main window when w.Target no longer names it, by the pane
// it holds (the overview, @role=ov) instead of its name, and renames that
// window back so every session:main caller works again. Without an overview
// pane in the session there is no wall to find: the error says so.
func (w *Wall) heal() error {
	session, name, _ := strings.Cut(w.Target, ":")
	panes, err := w.T.ListPanes("")
	if err != nil {
		return fmt.Errorf("find the main window: %w", err)
	}
	for _, p := range panes {
		if p.Session != session || p.Role != "ov" {
			continue
		}
		if _, err := w.T.Run("rename-window", "-t", p.WindowID, name); err != nil {
			return fmt.Errorf("rename window %s (%s) back to %s: %w", p.WindowID, p.Window, name, err)
		}
		logx.For("wall").Warn("main window renamed back", "window", p.WindowID, "was", p.Window, "name", name)
		return nil
	}
	return fmt.Errorf("no main window in tmux session %q (no overview pane): run `fleet up` to create it", session)
}
