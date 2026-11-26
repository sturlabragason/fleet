package mcp

import (
	"context"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/graph"
	"github.com/sturlabragason/fleet/internal/playbook"
)

// mapNote tells the admiral, in the spawn reply, when a code agent's repo
// has no fresh code map and how to get one (ADR-207). The agent hears the
// same in its prompt; nothing waits on it.
func (s *Server) mapNote(ag *board.Agent) string {
	if ag.Effort == string(playbook.Light) {
		return ""
	}
	if _, err := graph.Root(ag.Dir); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), fleet.GitTimeout)
	defer cancel()
	if _, err := graph.Fresh(ctx, s.F.Home, ag.Dir); err != nil {
		return "\ncode map: " + graph.MapMissing(err)
	}
	return ""
}
