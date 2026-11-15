package prefs

import (
	"fmt"
	"time"

	"github.com/sturlabragason/fleet/internal/conf"
)

// DefaultResurface is [reports] resurface_after: how long an agent's ask
// may wait on the user before its owner is woken about it again (ADR-130).
const DefaultResurface = 2 * time.Hour

// ResurfaceAfter is [reports] resurface_after; 0 turns the re-surface off.
func (p *Prefs) ResurfaceAfter() time.Duration {
	if p == nil {
		return DefaultResurface
	}
	return p.resurface
}

func applyReports(d time.Duration, sec map[string]conf.Value, path string) (time.Duration, error) {
	s, _ := sec["resurface_after"].(string)
	if s == "" {
		return d, nil
	}
	v, ok := conf.Dur(sec, "resurface_after")
	if !ok || v < 0 {
		return d, fmt.Errorf("%s: [reports] resurface_after %q: want a duration >= 0 such as \"2h\", or \"0\" for off", path, s)
	}
	return v, nil
}
