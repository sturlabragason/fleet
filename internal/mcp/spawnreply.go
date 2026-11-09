package mcp

import (
	"fmt"
	"strings"

	"github.com/sturlabragason/fleet/internal/board"
)

// Spawn replies (fleet#31): every fleet_spawn and fleet_squad_spawn reply
// names the profile and model the agent got, and a queued one says where it
// stands, so a wrong default or a full queue is seen at once.

// spawnProfile is the profile a spawn call uses: its own, else [defaults]
// profile from config.toml. list names the tool that lists the profiles.
func (s *Server) spawnProfile(p, list string) (string, error) {
	if p = strings.TrimSpace(p); p != "" {
		return p, nil
	}
	if d := s.F.DefaultProfile(); d != "" {
		return d, nil
	}
	return "", fmt.Errorf("profile is required: pass one (%s lists them), or set [defaults] profile in config.toml", list)
}

// spawnLabel is fleet's SpawnLabel: the profile and model.
func (s *Server) spawnLabel(ag *board.Agent) string { return s.F.SpawnLabel(ag) }

// queuedReply is the reply for a spawn that went into the queue.
func (s *Server) queuedReply(ag *board.Agent) string {
	return fmt.Sprintf("queued %s (%s) in %s: %d ahead of it, %d agents busy, max_working %d%s; it starts when a slot frees",
		ag.Name, s.spawnLabel(ag), ag.Dir, s.F.QueueAhead(ag.Name), s.F.Busy(), s.F.MaxWorking(), queueNote(ag))
}
