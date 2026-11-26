package fleet

import (
	"context"
	"strings"

	"github.com/sturlabragason/fleet/internal/graph"
	"github.com/sturlabragason/fleet/internal/playbook"
)

// graphBrief is the code-graph note after the prompt: where the code map is,
// for code work (standard and pro, ADR-207), and the callers of the repo
// paths the prompt names (ADR-165). Bounded like the other spawn-time git
// calls.
func (f *Fleet) graphBrief(dir, prompt string, lvl playbook.Level) string {
	if prompt == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), GitTimeout)
	defer cancel()
	note := ""
	if lvl != playbook.Light {
		note = graph.MapNote(ctx, f.Home, dir)
	}
	if b := graph.Brief(ctx, f.Home, dir, prompt); b != "" {
		note = strings.TrimSpace(note+"\n"+b) + "\n"
	}
	return note
}
