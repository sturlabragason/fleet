package web

import (
	"context"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/theme"
)

// WatchTheme reloads the theme files every tick until ctx ends (ADR-201), so
// the next page load draws an edited theme without restarting fleet web.
// The palette is shared with the page handler, so both hold themeMu.
func (s *Server) WatchTheme(ctx context.Context, r *theme.Reloader, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.themeMu.Lock()
		changed, warn := r.Check()
		s.themeMu.Unlock()
		if warn != "" {
			logx.For("web").Warn(warn)
		}
		if changed {
			logx.For("web").Info("theme reloaded", "theme", theme.Active().Name)
		}
	}
}
