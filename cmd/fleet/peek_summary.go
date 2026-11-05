package main

import (
	"context"
	"fmt"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/sessions"
)

// cmdPeekSummary prints a few lines about one agent from the cheap model
// (DESIGN §6.2, ADR-40). It fails open: an unreachable model is an error on
// this command, never a problem for the rest of fleet.
func cmdPeekSummary(f *fleet.Fleet, name string) error {
	r, err := f.PeekSummary(context.Background(), name)
	if err != nil {
		return fmt.Errorf("no summary for %s: %v (try fleet peek %s)", name, err, name)
	}
	fmt.Println(r.Text)
	switch {
	case r.Stale:
		fmt.Printf("\n(cached %s ago, the screen has moved on since)\n", sessions.Age(time.Since(r.At)))
	case r.Cached:
		fmt.Printf("\n(cached, the screen has not changed since %s ago)\n", sessions.Age(time.Since(r.At)))
	}
	return nil
}
