package fleet

import (
	"fmt"
	"sync"

	"github.com/sturlabragason/fleet/internal/tmux"
)

// capCache keeps each pane's last capture with the output generation and
// size it was taken at. A pane tmux has written nothing to since reads the
// same, so Refresh reuses the text instead of running capture-pane: an idle
// fleet then costs no capture at all (ADR-138).
type capCache struct {
	mu sync.Mutex
	by map[string]capEntry // pane id -> last capture
}

type capEntry struct{ key, text string }

// capKey is what must match for a capture to be reused; "" (a tmux without
// pane_output_generation) never matches, so every pass captures as before.
func capKey(p tmux.PaneInfo) string {
	if p.OutputGen == "" {
		return ""
	}
	return fmt.Sprintf("%s:%dx%d", p.OutputGen, p.Width, p.Height)
}

// capture is f.T.Capture(pane, 0) through the cache.
func (f *Fleet) capture(pane, key string) (string, error) {
	c := &f.caps
	if key != "" {
		c.mu.Lock()
		e, ok := c.by[pane]
		c.mu.Unlock()
		if ok && e.key == key {
			return e.text, nil
		}
	}
	text, err := f.T.Capture(pane, 0)
	if err != nil || key == "" {
		return text, err
	}
	c.mu.Lock()
	if c.by == nil {
		c.by = map[string]capEntry{}
	}
	c.by[pane] = capEntry{key: key, text: text}
	c.mu.Unlock()
	return text, nil
}

// keep drops the captures of panes that are no longer listed.
func (c *capCache) keep(panes []tmux.PaneInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.by) == 0 {
		return
	}
	live := make(map[string]bool, len(panes))
	for _, p := range panes {
		live[p.ID] = true
	}
	for id := range c.by {
		if !live[id] {
			delete(c.by, id)
		}
	}
}
