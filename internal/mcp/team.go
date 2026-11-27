package mcp

import (
	"fmt"
	"strings"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/status"
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
		if err := s.teamSendDialog(a); err != nil {
			return "", err
		}
		rs, err := s.F.TeamSend(a.Team, a.Text, a.Confirmed)
		for i := range rs {
			rs[i].Note = s.dialogHint(rs[i].Note)
		}
		return results("sent", rs, err)
	case "show":
		rs, err := s.F.TeamShow(s.W, a.Team)
		return results("on wall", rs, err)
	case "hide":
		rs, err := s.F.TeamHide(s.W, a.Team)
		return results("off wall", rs, err)
	case "close":
		if err := s.confirm(gate{tool: "fleet_team", ask: "Close every agent in team " + a.Team + "?"}, a); err != nil {
			return "", err
		}
		rs, err := s.F.TeamClose(a.Team)
		return results("closed", rs, err)
	}
	return "", fmt.Errorf("action must be one of list, create, delete, add, remove, color, send, show, hide, close")
}

// teamSendDialog is team send's gate for a client with fleet's dialog:
// confirmed=true asks to answer the members' prompts, so the user is asked
// once, and only when the team exists and a member is on a prompt.
func (s *Server) teamSendDialog(a args) error {
	if !a.Confirmed || !s.elicits() {
		return nil
	}
	ms, err := s.F.Teams.Members(a.Team)
	if err != nil {
		return err
	}
	var on []string
	for _, m := range ms {
		if w, err := s.F.Prompting(m); err == nil && w != "" {
			on = append(on, m+" ("+status.Cut(w, 60)+")")
		}
	}
	if len(on) == 0 {
		return nil
	}
	return s.confirm(gate{tool: "fleet_team", ask: "Send to team " + a.Team + "? This answers the prompts of " + strings.Join(on, ", ") + "."}, a)
}
