package fleet

import (
	"context"

	"github.com/sturlabragason/fleet/internal/graph"
)

// graphBrief is the code-graph note after the prompt (ADR-165): the callers
// of the repo paths it names. Bounded like the other spawn-time git calls.
func (f *Fleet) graphBrief(dir, prompt string) string {
	if prompt == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), GitTimeout)
	defer cancel()
	return graph.Brief(ctx, f.Home, dir, prompt)
}
