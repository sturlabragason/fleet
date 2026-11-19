package fleet

import (
	"context"
	"strings"

	"github.com/sturlabragason/fleet/internal/todo"
	"github.com/sturlabragason/fleet/internal/waits"
)

// Extension is what a feature compiled in with a build tag adds to this
// package (DESIGN §15 "Extensions", ADR-126). It calls Register from its own
// init(); a default build registers nothing.
type Extension struct {
	Name string
	// Wake runs at the end of DeliverWake.
	Wake func(f *Fleet)
	// AdmiralBadge is extra border text per admiral name (pane option
	// @fleet_admiral_ext); a missing name shows nothing.
	AdmiralBadge func(f *Fleet) map[string]string
	// Todo opens the store for admiral open items and agent queues
	// (ADR-164); the first extension that sets it is used.
	Todo func() (todo.Store, error)
	// Waits adds items to the "waits on you" list (ADR-167), e.g. open
	// threads with their next step.
	Waits func(f *Fleet) []waits.Item
}

var exts []Extension

// Register adds an extension. Call it from init(); every registered
// extension's hooks run, in registration order.
func Register(e Extension) {
	if e.Name == "" {
		panic("fleet.Register: extension has no name")
	}
	for _, x := range exts {
		if x.Name == e.Name {
			panic("fleet.Register: extension " + e.Name + " registered twice")
		}
	}
	exts = append(exts, e)
}

func extWake(f *Fleet) {
	for _, e := range exts {
		if e.Wake != nil {
			e.Wake(f)
		}
	}
}

// extAdmiralBadge joins every extension's badge for an admiral with a space.
func extAdmiralBadge(f *Fleet) map[string]string {
	var out map[string]string
	for _, e := range exts {
		if e.AdmiralBadge == nil {
			continue
		}
		for a, s := range e.AdmiralBadge(f) {
			if s == "" {
				continue
			}
			if out == nil {
				out = map[string]string{}
			}
			out[a] = strings.TrimSpace(out[a] + " " + s)
		}
	}
	return out
}

// TodoDo runs fn on the registered todo store (ADR-164); without one it is
// ok false with no error.
func TodoDo(fn func(ctx context.Context, s todo.Store) error) (ok bool, err error) {
	for _, e := range exts {
		if e.Todo != nil {
			return todo.Do(e.Todo, fn)
		}
	}
	return false, nil
}
