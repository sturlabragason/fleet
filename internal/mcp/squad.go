package mcp

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/status"
)

// LeaderInstructions are appended to the agent instructions for a leader
// (DESIGN §6.11, ADR-53).
//
//go:embed leader_instructions.md
var LeaderInstructions string

// Squad tools (DESIGN §6.11, ADR-53). A leader gets these on top of the
// ordinary agent tools: they are the admiral's tools narrowed to the agents
// the leader spawned itself. Deliberately missing: anything that answers a
// prompt, merges, pushes or applies — those escalate to the admiral.

var squadToolDefs = []tool{
	{"fleet_squad_spawn", "Start an agent of your own squad: same tool profiles, worktrees and task cards as the admiral's spawns. In a git repo it gets its own worktree <repo>.wt/<name> on branch fleet/<name>. Its reports come to your inbox, not the admiral's. Capped by [squad] max_members and the fleet-wide max_working (over the cap it queues).", obj([]string{"name", "dir"}, map[string]any{
		"name": str("short unique agent name"), "profile": str("tool profile, see fleet_squad_profiles; default [defaults] profile in config.toml"), "dir": str("working directory (absolute)"), "prompt": str("optional first prompt"),
		"worktree":   map[string]any{"type": "boolean", "description": "give the agent its own git worktree (default: the user's config, normally true)"},
		"goal":       str("task card: the goal in one line"),
		"check":      str("task card: done-check shell command, run in the agent's dir when it reports done"),
		"check_exit": map[string]any{"type": "integer", "description": "exit code the done-check must return to pass (default 0)"},
		"result":     str("task card: result file the agent must write, relative to its dir (default result.md)"),
		"may":        mayProp,
		"driver":     map[string]any{"type": "string", "enum": []string{"tmux", "acp"}, "description": "how fleet runs the agent: tmux (the tool's own screen) or acp (the tool's ACP server under fleet acp-host: status, questions and sends over the protocol, no screen reading). Default: the user's config, else acp for every profile that runs copilot or agy (claude-copilot too) and tmux for the rest. Claude runs over acp only when asked (this field or the profile's config), never from [defaults]; vibe refuses acp."},
		"effort":     map[string]any{"type": "string", "enum": []string{"light", "standard", "pro"}, "description": "how much process the agent is given: light (an errand or a quick read-only question), standard (a contained change: plan, build, test, commit), pro (the full playbook, including the pre-merge checklist). Default: pro with a worktree, light without one."}})},
	{"fleet_squad_profiles", "List the tool profiles fleet_squad_spawn accepts.", obj(nil, map[string]any{})},
	{"fleet_squad_status", "List your squad: name, profile, status, age, what each agent waits for and its last report. Start here.", obj(nil, map[string]any{})},
	{"fleet_squad_info", "Facts about one of your agents: profile, status, dir, task card and its done-check verdict, git state, spend.", obj([]string{"name"}, map[string]any{
		"name": str("squad agent name")})},
	{"fleet_squad_peek", "Show the last N lines (default 20, max 80) of one of your agents' screens.", obj([]string{"name"}, map[string]any{
		"name": str("squad agent name"), "lines": map[string]any{"type": "integer", "description": "lines to return (default 20, max 80)"},
		"raw": map[string]any{"type": "boolean", "description": "screen as captured, with borders"}})},
	{"fleet_squad_send", "Send a prompt or follow-up to one of your agents, or \"*\" for all of them. An agent sitting on a permission prompt is refused: you may not answer prompts, report it to the admiral instead.", obj([]string{"name", "text"}, map[string]any{
		"name": str("squad agent name, or * for all of them"), "text": str("text to send")})},
	{"fleet_squad_reports", "Read your agents' reports (done, blocked, question, progress) without marking them read; fleet_inbox is where you mark them read. name=<agent> shows only that agent's.", obj(nil, map[string]any{
		"name": str("only this squad agent's reports"), "limit": map[string]any{"type": "integer", "description": "how many to show (default 20)"}})},
	{"fleet_squad_close", "Stop one of your agents and take it off the board, or with name=\"*\" all of them. Your call, not the user's: once its work is merged or no longer needed (fleet_squad_info shows it), pass confirmed=true and it closes on the first call; the reply still names any uncommitted or unmerged work left behind. confirmed=false only lists that work and closes nothing.", obj([]string{"name", "confirmed"}, map[string]any{
		"name": str("squad agent name, or * for all of them"), "confirmed": map[string]any{"type": "boolean", "description": "true closes now; false only lists the work the close would leave behind"}})},
}

// isLeader reports whether the agent this server acts for leads a squad.
func (s *Server) isLeader() bool {
	me, err := s.F.Me(s.Agent)
	if err != nil {
		return false
	}
	return s.F.IsLeader(me)
}

// agentTools are the tools an agent sees: the peer tools, plus the squad
// tools when it is a leader.
func (s *Server) agentTools() []tool {
	if !s.isLeader() {
		return agentToolDefs
	}
	return append(append([]tool(nil), agentToolDefs...), squadToolDefs...)
}

func isSquadTool(name string) bool {
	for _, t := range squadToolDefs {
		if t.Name == name {
			return true
		}
	}
	return false
}

// squadCall dispatches a squad tool. Every tool that names an agent goes
// through SquadMember first, so a leader can only ever act on the agents it
// spawned.
func (s *Server) squadCall(me, name string, raw json.RawMessage) (string, error) {
	var a args
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", fmt.Errorf("bad arguments: %v", err)
		}
	}
	if !s.F.IsLeader(me) {
		return "", fmt.Errorf("%s does not lead a squad", me)
	}
	switch name {
	case "fleet_squad_spawn":
		return s.squadSpawn(me, a)
	case "fleet_squad_profiles":
		return s.profiles(), nil
	case "fleet_squad_status":
		return s.squadStatus(me), nil
	case "fleet_squad_reports":
		return s.squadReports(me, a.Name, a.Limit), nil
	case "fleet_squad_send":
		return s.squadSend(me, a)
	case "fleet_squad_close":
		return s.squadClose(me, a)
	}
	if _, err := s.F.SquadMember(me, a.Name); err != nil {
		return "", err
	}
	switch name {
	case "fleet_squad_info":
		return s.info(a)
	default: // fleet_squad_peek
		return s.peek(args{Name: a.Name, Lines: a.Lines, Raw: a.Raw})
	}
}

func (s *Server) squadSpawn(me string, a args) (string, error) {
	for f, v := range map[string]string{"name": a.Name, "dir": a.Dir} {
		if v == "" {
			return "", fmt.Errorf("%s is required", f)
		}
	}
	prof, err := s.spawnProfile(a.Profile, "fleet_squad_profiles")
	if err != nil {
		return "", err
	}
	ag, err := s.F.SquadSpawn(me, fleet.SpawnOpts{Worktree: a.Worktree, Card: a.card(), Effort: a.Effort, Driver: a.Driver}, a.Name, prof, a.Dir, a.Prompt)
	if err != nil {
		return "", err
	}
	if ag.Status == string(status.Queued) {
		return s.queuedReply(ag) + cardNote(ag), nil
	}
	if w := ag.Worktree; w != nil {
		return fmt.Sprintf("spawned %s (%s) into your squad, in its own worktree %s on branch %s", ag.Name, s.spawnLabel(ag), w.Path, w.Branch) + cardNote(ag), nil
	}
	return fmt.Sprintf("spawned %s (%s) into your squad, in %s", ag.Name, s.spawnLabel(ag), ag.Dir) + cardNote(ag), nil
}

func (s *Server) squadStatus(me string) string {
	s.F.Refresh()
	ms := s.F.Squad(me)
	if len(ms) == 0 {
		return "your squad is empty; spawn an agent with fleet_squad_spawn"
	}
	var b strings.Builder
	b.WriteString("name | profile | status | age | waiting_for | report\n")
	for _, a := range ms {
		wf := ""
		if a.Status == "waiting" || a.Status == "error" || a.Status == "stuck" {
			wf = s.waitingFor(a)
		}
		fmt.Fprintf(&b, "%s | %s | %s | %s | %s | %s\n", a.Name, a.Profile, a.Status,
			time.Since(a.StatusSince).Round(time.Second), wf, reportCell(a))
	}
	return b.String()
}

// squadReports shows the reports in the leader's own inbox without marking
// them read: fleet_inbox is the one place that does that, so a leader sees
// each report exactly once there however often it looks here.
func (s *Server) squadReports(me, name string, limit int) string {
	if limit <= 0 {
		limit = 20
	}
	var ms []mail.Msg
	for _, m := range s.F.Mail().Peek(me) {
		if m.Kind == mail.KindReport && (name == "" || m.From == name) {
			ms = append(ms, m)
		}
	}
	if len(ms) > limit {
		ms = ms[len(ms)-limit:]
	}
	if len(ms) == 0 {
		if name != "" {
			return "no unread reports from " + name
		}
		return "no unread reports from your squad"
	}
	return RenderReports(ms, true) + taskCards(inReports(s.F.Squad(me), ms))
}

// squadSend is fleet_squad_send: plain text only, and never into a prompt.
// Answering a permission prompt is the admiral's and the user's call
// (ADR-53), so a leader is refused rather than given a confirmed=true out.
func (s *Server) squadSend(me string, a args) (string, error) {
	if strings.TrimSpace(a.Text) == "" {
		return "", fmt.Errorf("text is required")
	}
	if a.Name == "" {
		return "", fmt.Errorf("name is required (\"*\" sends to the whole squad)")
	}
	names := []string{a.Name}
	if a.Name == "*" {
		names = nil
		for _, m := range s.F.Squad(me) {
			names = append(names, m.Name)
		}
		if len(names) == 0 {
			return "", fmt.Errorf("your squad is empty")
		}
	}
	var rs []fleet.Result
	for _, n := range names {
		r := fleet.Result{Agent: n, OK: true}
		if m, err := s.F.SquadMember(me, n); err != nil {
			if len(names) == 1 {
				return "", err
			}
			r.OK, r.Note = false, "not in your squad"
		} else if m.Status == string(status.Waiting) {
			r.OK, r.Note = false, "on a prompt; report it to the admiral, you may not answer prompts"
		} else if held, err := s.sendOne(args{Name: n, Text: a.Text}, n); err != nil {
			r.OK, r.Note = false, err.Error()
		} else if held && len(names) == 1 {
			return fleet.HeldNote(n), nil
		} else if held {
			r.Agent += " (queued: delivered when it starts)"
		}
		rs = append(rs, r)
	}
	return fleet.FormatResults("sent", rs), nil
}

// squadClose is fleet_close for a leader's own agents. The leader decides,
// not the user, so confirmed=true closes on the first call and the reply
// still names the work left behind; confirmed=false only lists that work
// (ADR-29, ADR-53, fleet#33).
func (s *Server) squadClose(me string, a args) (string, error) {
	if a.Name == "" {
		return "", fmt.Errorf("name is required (\"*\" closes the whole squad)")
	}
	names := []string{a.Name}
	if a.Name == "*" {
		names = nil
		for _, m := range s.F.Squad(me) {
			names = append(names, m.Name)
		}
		if len(names) == 0 {
			return "your squad is empty", nil
		}
	} else if _, err := s.F.SquadMember(me, a.Name); err != nil {
		return "", err
	}
	var notes []string
	for _, n := range names {
		if note := s.F.CloseNote(n); note != "" {
			notes = append(notes, n+": "+note)
		}
	}
	if !a.Confirmed {
		msg := "not confirmed: call again with confirmed=true to close " + strings.Join(names, ", ")
		if len(notes) > 0 {
			msg += ". " + strings.Join(notes, "; ")
		}
		return "", fmt.Errorf("%s", msg)
	}
	if a.Name == "*" {
		return fleet.FormatResults("closed", s.F.SquadCloseAll(me)), nil
	}
	if err := s.F.Close(a.Name); err != nil {
		return "", err
	}
	out := "closed " + a.Name
	if len(notes) > 0 {
		out += ". " + notes[0]
	}
	return out, nil
}

// leaderNote tells the admiral what a leader spawn just created.
func leaderNote(ag *board.Agent) string {
	if !ag.Leader {
		return ""
	}
	return "\nIt is a leader: it has squad tools of its own, its agents' reports go to it, and it reports one summary to you."
}

// squadCell is the fleet_status squad column: who an agent leads, or who
// leads it.
func squadCell(f *fleet.Fleet, a *board.Agent) string {
	switch {
	case a.Leader:
		ms := f.Squad(a.Name)
		if len(ms) == 0 {
			return "leader (0)"
		}
		var ns []string
		for _, m := range ms {
			ns = append(ns, m.Name)
		}
		return "leads " + strings.Join(ns, ",")
	case a.LeadBy != "":
		return "→ " + a.LeadBy
	}
	return ""
}
