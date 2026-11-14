//go:build extexample

package fleet

import (
	"sync"
	"sync/atomic"
)

// The public example extension (DESIGN §15 "Extensions"): it fills every
// seam of this package so public CI compiles and tests them. It stays inert
// for a *Fleet until a test turns it on with exampleOn, and keeps its state
// beside the Fleet, keyed by the pointer, as a real extension does.
type exampleState struct {
	wakes atomic.Int32
	badge map[string]string
}

var exampleStates sync.Map // *Fleet -> *exampleState

func exampleOn(f *Fleet) *exampleState {
	v, _ := exampleStates.LoadOrStore(f, &exampleState{})
	return v.(*exampleState)
}

func exampleOf(f *Fleet) *exampleState {
	if v, ok := exampleStates.Load(f); ok {
		return v.(*exampleState)
	}
	return nil
}

func init() {
	Register(Extension{
		Name: "example",
		Wake: func(f *Fleet) {
			if st := exampleOf(f); st != nil {
				st.wakes.Add(1)
			}
		},
		AdmiralBadge: func(f *Fleet) map[string]string {
			if st := exampleOf(f); st != nil {
				return st.badge
			}
			return nil
		},
	})
}
