package mcp

import (
	"fmt"
	"strings"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/tools"
)

// admiralHeader is the first line of fleet_status and fleet_brief
// (DESIGN §6.10): `admiral: claude-personal · session 3f2a91c · gen 3`.
func (s *Server) admiralHeader() string {
	cur := supervisor.CurrentOf(s.F.Home, s.F.T, fleet.Session+":main", s.Me())
	line := admiralLine(cur)
	if s.Admiral != "" || len(supervisor.Admirals(s.F.Home)) > 0 { // several admirals: say which one you are
		line = strings.Replace(line, "admiral:", "admiral "+s.Me()+":", 1)
	}
	return line
}

func admiralLine(cur supervisor.Session) string {
	prof := cur.Profile
	if prof == "" {
		prof = "?"
	}
	parts := []string{"admiral: " + prof}
	if id := cur.SessionID; id != "" {
		if len(id) > 7 {
			id = id[:7]
		}
		parts = append(parts, "session "+id)
	}
	if cur.Generation > 0 {
		parts = append(parts, fmt.Sprintf("gen %d", cur.Generation))
	}
	return strings.Join(parts, " · ")
}

// admiralProfile checks the name, writes summary as the handoff and
// schedules the swap; it returns at once because the swap replaces the pane
// this server runs in.
func (s *Server) admiralProfile(name, summary string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("profile is required")
	}
	if _, ok := s.F.Profiles[name]; !ok {
		return "", fmt.Errorf("no profile %q; have: %s", name, strings.Join(tools.Names(s.F.Profiles), ", "))
	}
	if strings.TrimSpace(summary) != "" {
		if err := supervisor.WriteHandoffOf(s.F.Home, s.Me(), summary); err != nil {
			return "", err
		}
	}
	if err := s.SwapProfile(name); err != nil {
		return "", fmt.Errorf("could not schedule the swap: %v", err)
	}
	return fmt.Sprintf("switching admiral to %s in a few seconds. Stop here.", name), nil
}
