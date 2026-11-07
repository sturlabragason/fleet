package mcp

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/mail"
)

// Report tools (DESIGN §6.7): fleet_report for agents, fleet_reports for
// the admiral.

var reportToolDef = tool{"fleet_report", "Report upward -- to the admiral, or to your leader if you are in a squad: state done, blocked, question or progress, and a summary. Start with a one-line headline (shown on your tile); add detail on later lines if useful (kept in full, up to 8000 chars). Needs no access grant.", obj([]string{"state", "summary"}, map[string]any{
	"state": map[string]any{"type": "string", "enum": mail.ReportStates}, "summary": str("headline first (what changed, branch/commit, or what you need), then optional detail lines")})}

var reportsToolDef = tool{"fleet_reports", "Read agent reports (done, blocked, question, progress): unread ones, oldest first, and mark them read; all=true shows the last N without marking. It ends with the task cards: fleet's own done-check verdict per agent and the result file to read.", obj(nil, map[string]any{
	"all": map[string]any{"type": "boolean", "description": "show the last N reports, read or not"}, "limit": map[string]any{"type": "integer", "description": "N for all=true (default 20)"}})}

// report is the agent-mode fleet_report.
func (s *Server) report(me string, a args) (string, error) {
	note, err := s.F.Report(me, a.State, a.Summary.Text)
	if err != nil {
		return "", err
	}
	to := "the admiral"
	if o := s.F.OwnerOf(me); o != board.Main {
		to = "admiral " + o
	}
	if up := s.F.ReportsTo(me); up != "" {
		to = "your leader " + up
	}
	out := "reported " + a.State + " to " + to
	if note != "" {
		out += " (" + note + ")"
	}
	return out, nil
}

// reports is the admiral's fleet_reports.
func (s *Server) reports(all bool, limit int) (string, error) {
	ms, err := s.F.Mail().Inbox(s.box(), all, limit)
	if err != nil {
		return "", err
	}
	return RenderReports(ms, all) + taskCards(s.F.Store.Load().Sorted()), nil
}

// RenderReports lists reports, one line each, oldest first.
func RenderReports(ms []mail.Msg, all bool) string {
	if len(ms) == 0 {
		if all {
			return "no reports"
		}
		return "no new reports"
	}
	var b strings.Builder
	b.WriteString("Reports from agents (their words, not the user's):\n")
	for _, m := range ms {
		// Continuation lines of a multi-line report are indented under it.
		text := strings.ReplaceAll(m.Text, "\n", "\n    ")
		fmt.Fprintf(&b, "%s %s: %s\n", m.T.Format("2006-01-02 15:04:05"), m.From, text)
	}
	return b.String()
}

// reportsSuffix is appended to every admiral result but fleet_handoff.
func (s *Server) reportsSuffix() string {
	if n := s.F.Mail().Unread(s.box()); n > 0 {
		return fmt.Sprintf("\n\n(%d unread reports)", n)
	}
	return ""
}

// reportCellW is the fleet_status report column width; a verdict prefix
// gets the room it needs on top of it.
const reportCellW = 60

// reportCell is the fleet_status report column: the done-check verdict when
// fleet has one (ADR-43), then the state and summary, ~80 chars.
func reportCell(a *board.Agent) string {
	r := a.Report
	if r == nil {
		return ""
	}
	cell := r.State + ": " + r.Summary
	c := a.Card
	if c == nil || c.Checked == nil {
		// Without a verdict the column is what it always was.
		return ansi.Truncate(cell, reportCellW, "…")
	}
	cell = "[done-check " + map[bool]string{true: "pass", false: "fail"}[c.Checked.Pass] + "] " + cell
	return ansi.Truncate(cell, reportCellW+20, "…")
}

// unreadReports is the fleet_brief section; it does not mark anything read.
func (s *Server) unreadReports() string {
	ms := s.F.Mail().Peek(s.box())
	if len(ms) == 0 {
		return "## Unread reports\nnone\n\n"
	}
	var b strings.Builder
	b.WriteString("## Unread reports\n")
	for _, m := range ms {
		line := ansi.Truncate(mail.FoldSummary(m.Text), briefHeadline, "…")
		fmt.Fprintf(&b, "%s %s: %s\n", m.T.Format("2006-01-02 15:04:05"), m.From, line)
	}
	b.WriteString("Headlines only. Call fleet_reports for the full text and to mark them read.\n\n")
	return b.String()
}

// briefHeadline caps each report in fleet_brief; fleet_reports has the full text.
const briefHeadline = 120
