package mcp

import (
	"fmt"
	"strings"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/team"
)

// team runs one fleet_team action.
func (s *Server) team(a args) (string, error) {
	ts := s.F.Teams
	if a.Action != "list" && a.Team == "" {
		return "", fmt.Errorf("team is required")
	}
	results := func(verb string, rs []fleet.Result, err error) (string, error) {
		if err != nil {
			return "", err
		}
		return a.Team + ": " + fleet.FormatResults(verb, rs), nil
	}
	switch a.Action {
	case "add", "remove":
		if err := s.scope(a.Agents...); err != nil {
			return "", err
		}
		if err := s.teamScope(a.Team); err != nil {
			return "", err
		}
	case "send", "close", "show", "hide", "delete", "color":
		if err := s.teamScope(a.Team); err != nil {
			return "", err
		}
	}
	switch a.Action {
	case "list":
		rows := s.F.TeamList()
		if len(rows) == 0 {
			return "no teams", nil
		}
		var b strings.Builder
		b.WriteString("team | color | members\n")
		for _, r := range rows {
			var ms []string
			for _, m := range r.Members {
				if m.Running {
					ms = append(ms, m.Name)
				} else {
					ms = append(ms, m.Name+" (not running)")
				}
			}
			fmt.Fprintf(&b, "%s | %s | %s\n", r.Name, team.ColorName(r.Color), strings.Join(ms, ", "))
		}
		return b.String(), nil
	case "create":
		return "created team " + a.Team, ts.Create(a.Team, a.Color)
	case "delete":
		return "deleted team " + a.Team + " (agents keep running)", ts.Delete(a.Team)
	case "add":
		if len(a.Agents) == 0 {
			return "", fmt.Errorf("agents is required")
		}
		moved, err := ts.Add(a.Team, a.Agents...)
		if err != nil {
			return "", err
		}
		return "added " + strings.Join(a.Agents, ", ") + " to " + a.Team + fleet.MovedNote(moved), nil
	case "remove":
		if len(a.Agents) == 0 {
			return "", fmt.Errorf("agents is required")
		}
		return "removed " + strings.Join(a.Agents, ", ") + " from " + a.Team, ts.Remove(a.Team, a.Agents...)
	case "color":
		if a.Color == "" {
			return "", fmt.Errorf("color is required")
		}
		return a.Team + " is now " + a.Color, ts.SetColor(a.Team, a.Color)
	case "send":
		if a.Text == "" {
			return "", fmt.Errorf("text is required")
		}
		rs, err := s.F.TeamSend(a.Team, a.Text, a.Confirmed)
		return results("sent", rs, err)
	case "show":
		rs, err := s.F.TeamShow(s.W, a.Team)
		return results("on wall", rs, err)
	case "hide":
		rs, err := s.F.TeamHide(s.W, a.Team)
		return results("off wall", rs, err)
	case "close":
		if !a.Confirmed {
			return "", fmt.Errorf("not confirmed: ask the user, then call again with confirmed=true")
		}
		rs, err := s.F.TeamClose(a.Team)
		return results("closed", rs, err)
	}
	return "", fmt.Errorf("action must be one of list, create, delete, add, remove, color, send, show, hide, close")
}
