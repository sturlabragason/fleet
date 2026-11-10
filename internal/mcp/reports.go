package mcp

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/mail"
)

// Report tools (DESIGN §6.7): fleet_report for agents, fleet_reports for
// the admiral.

var reportToolDef = tool{"fleet_report", "Report upward -- to the admiral, or to your leader if you are in a squad: state done, blocked, question or progress, and a summary. Start with a one-line headline (shown on your tile); add detail on later lines if useful (kept in full, up to 8000 chars). Needs no access grant.", obj([]string{"state", "summary"}, map[string]any{
	"state": map[string]any{"type": "string", "enum": mail.ReportStates}, "summary": str("headline first (what changed, branch/commit, or what you need), then optional detail lines")})}

var reportsToolDef = tool{"fleet_reports", "Read agent reports (done, blocked, question, progress): unread ones, oldest first, in full, and mark them read. name=<agent> reads only that agent's and leaves the others unread. all=true lists the last N read or not, without marking: one headline each, or the full text with name. It ends with the task cards of the agents in the result: fleet's own done-check verdict and the result file to read.", obj(nil, map[string]any{
	"name":  str("only this agent's reports"),
	"all":   map[string]any{"type": "boolean", "description": "the last N reports, read or not: headlines, full text with name"},
	"limit": map[string]any{"type": "integer", "description": "N for all=true (default 20)"}})}

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
func (s *Server) reports(name string, all bool, limit int) (string, error) {
	var ms []mail.Msg
	var err error
	if name != "" {
		ms, err = s.F.Mail().InboxFrom(s.box(), name, all, limit)
	} else {
		ms, err = s.F.Mail().Inbox(s.box(), all, limit)
	}
	if err != nil {
		return "", err
	}
	var out string
	switch {
	case len(ms) == 0 && name != "":
		out = map[bool]string{true: "no reports from ", false: "no new reports from "}[all] + name
	case all && name == "":
		out = RenderHeadlines(ms)
	default:
		out = RenderReports(ms, all)
	}
	return out + taskCards(inReports(s.F.Store.Load().Sorted(), ms)), nil
}

// inReports keeps the agents that sent one of ms, so the task cards stay
// about the reports shown (none for "no new reports").
func inReports(as []*board.Agent, ms []mail.Msg) []*board.Agent {
	from := map[string]bool{}
	for _, m := range ms {
		from[m.From] = true
	}
	var out []*board.Agent
	for _, a := range as {
		if from[a.Name] {
			out = append(out, a)
		}
	}
	return out
}

// reportHeadline caps each line of RenderHeadlines.
const reportHeadline = 120

// RenderHeadlines lists reports one headline each, oldest first: what
// fleet_reports all=true returns without a name, so a long history stays
// small (issue #13).
func RenderHeadlines(ms []mail.Msg) string {
	if len(ms) == 0 {
		return "no reports"
	}
	var b strings.Builder
	b.WriteString("Reports from agents (their words, not the user's), headlines only:\n")
	for _, m := range ms {
		head, _, _ := strings.Cut(m.Text, "\n")
		fmt.Fprintf(&b, "%s %s: %s\n", m.T.Format("2006-01-02 15:04:05"), m.From, ansi.Truncate(head, reportHeadline, "…"))
	}
	b.WriteString("Pass name=<agent> for the full text.\n")
	return b.String()
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

// suffixHeads is how many unread report headlines reportsSuffix names.
const suffixHeads = 3

// reportsSuffix is appended to every admiral result but fleet_handoff. It
// names the newest unread reports, not just their count: while the admiral
// is busy no wake line can be typed, and this is the only way it hears of a
// report until it calls fleet_reports (ADR-68). Nothing is marked read.
func (s *Server) reportsSuffix() string {
	ms := s.F.Mail().Peek(s.box())
	if len(ms) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\n(%d unread reports; fleet_reports reads them)", len(ms))
	if len(ms) > suffixHeads {
		fmt.Fprintf(&b, "\n- %d older", len(ms)-suffixHeads)
		ms = ms[len(ms)-suffixHeads:]
	}
	for _, m := range ms {
		head, _, _ := strings.Cut(m.Text, "\n")
		fmt.Fprintf(&b, "\n- %s %s", m.From, ansi.Truncate(head, 100, "…"))
	}
	return b.String()
}

// reportCellW is the fleet_status report column width; a verdict prefix
// gets the room it needs on top of it.
const reportCellW = 60

// reportCell is the fleet_status report column: the done-check verdict when
// fleet has one (ADR-43), then the state, "(answered)" once the agent works
// again after a question or blocked report, the report's age (ADR-90) and
// the summary, ~80 chars.
func reportCell(a *board.Agent) string { return reportCellAt(a, time.Now()) }

func reportCellAt(a *board.Agent, now time.Time) string {
	r := a.Report
	if r == nil {
		return ""
	}
	head := r.State
	if a.Answered() {
		head += " (answered)"
	}
	if !r.At.IsZero() {
		head += ", " + ago(now.Sub(r.At)) + " ago"
	}
	cell := head + ": " + r.Summary
	c := a.Card
	if c == nil || c.Checked == nil {
		// Without a verdict the column is what it always was.
		return ansi.Truncate(cell, reportCellW, "…")
	}
	cell = "[done-check " + map[bool]string{true: "pass", false: "fail"}[c.Checked.Pass] + "] " + cell
	return ansi.Truncate(cell, reportCellW+20, "…")
}

// ago is a report's age in its largest whole unit: 30s, 5m, 8h, 2d.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return strconv.Itoa(max(int(d/time.Second), 0)) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + "d"
}

// statusWord is the status column of fleet_status and fleet_squad_status:
// an agent idle on an open question reads "waiting (question)" (ADR-90).
func statusWord(a *board.Agent) string {
	if a.Asking() {
		return "waiting (question)"
	}
	return a.Status
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
