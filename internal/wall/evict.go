package wall

import (
	"sort"

	"github.com/sturlabragason/fleet/internal/tmux"
)

// Eviction classes, best eviction candidate last (DESIGN.md §5b.1, ADR-14).
const (
	classPinned    = 0 // @fleet_pin = 1: never evicted
	classAttention = 1 // waiting/error/stuck: never evicted
	classWorking   = 2 // evicted only when nothing idle is left
	classIdle      = 3 // idle/gone/unknown/untagged: goes first
)

// needsAttention reports whether a pane is asking for me or has failed.
func needsAttention(st string) bool {
	switch st {
	case "waiting", "error", "stuck":
		return true
	}
	return false
}

// evictClass classifies one wall pane.
func evictClass(p tmux.PaneInfo) int {
	switch {
	case p.Pin:
		return classPinned
	case needsAttention(p.Status):
		return classAttention
	case p.Status == "working":
		return classWorking
	}
	return classIdle
}

// EvictCandidate returns the wall pane that should leave: the least recently
// used pane (@fleet_used) of the worst class present. ok is false when every
// pane is protected (pinned, waiting, error or stuck).
func EvictCandidate(ps []tmux.PaneInfo) (string, bool) {
	c := append([]tmux.PaneInfo(nil), ps...)
	sort.SliceStable(c, func(i, j int) bool {
		ci, cj := evictClass(c[i]), evictClass(c[j])
		if ci != cj {
			return ci > cj // worst class first
		}
		return c[i].Used < c[j].Used
	})
	if len(c) == 0 || evictClass(c[0]) <= classAttention {
		return "", false
	}
	return c[0].ID, true
}
