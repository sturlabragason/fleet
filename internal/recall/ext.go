package recall

import (
	"context"
	"strings"
)

// Extension is what a feature compiled in with a build tag adds to the day
// digest (DESIGN §15 "Extensions", ADR-127). It calls Register from its own
// init(); a default build registers nothing.
type Extension struct {
	Name string
	// Day is a section ("## ...") appended to the digest after "Sessions
	// outside fleet"; "" adds nothing.
	Day func(ctx context.Context, o DayOpts) string
}

var exts []Extension

// Register adds an extension. Call it from init(); every registered
// extension's hooks run, in registration order. A name registered twice
// panics.
func Register(e Extension) {
	if e.Name == "" {
		panic("recall.Register: extension has no name")
	}
	for _, x := range exts {
		if x.Name == e.Name {
			panic("recall.Register: extension " + e.Name + " registered twice")
		}
	}
	exts = append(exts, e)
}

// extDay joins every extension's day section, each after a blank line.
func extDay(ctx context.Context, o DayOpts) string {
	var b strings.Builder
	for _, e := range exts {
		if e.Day == nil {
			continue
		}
		if s := strings.TrimSpace(e.Day(ctx, o)); s != "" {
			b.WriteString("\n" + s + "\n")
		}
	}
	return b.String()
}
