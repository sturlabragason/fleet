package mcp

import (
	"context"

	"github.com/sturlabragason/fleet/internal/browser"
)

// presentCall is fleet_wall's present actions: the manual presenting
// switch and the blocked opens to hand over (ADR-200).
func (s *Server) presentCall(action string) (string, error) {
	if action != "present" {
		if err := browser.SetPresent(s.F.Home, action == "present_on"); err != nil {
			return "", err
		}
	}
	return browser.Status(context.Background(), s.F.Home, browser.Detect)
}
