package mcp

import (
	"fmt"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/routes"
	"github.com/sturlabragason/fleet/internal/supervisor"
)

// briefBudget keeps fleet_brief inline: an MCP client saves a larger tool
// result to a file the admiral may have no tool to read (Copilot CLI's limit
// is about 20 KB). The handoff and pinned notes are never cut; the other
// sections share what they leave and each falls back to a pointer.
const briefBudget = 12 << 10

// pinnedWarn is when the pinned section alone crowds out the rest of the
// brief and the admiral should ask the user to retire rules.
const pinnedWarn = briefBudget / 2

// maxBriefNotes is the most recent notes the brief shows when they fit.
const maxBriefNotes = 30

// brief is the handoff and every pinned note in full, then the unread report
// headlines, extensions, routes, recent notes and agents within briefBudget.
func (s *Server) brief() (string, error) {
	var head strings.Builder
	if h, at, ok := supervisor.ReadHandoffOf(s.F.Home, s.Me()); ok {
		fmt.Fprintf(&head, "## Handoff (%s)\n%s\n\n", at.Format("2006-01-02 15:04"), h)
	} else {
		head.WriteString("## Handoff\nnone\n\n")
	}
	pinned := supervisor.Pinned(s.F.Home)
	if len(pinned) > 0 {
		p := strings.Join(pinned, "\n")
		head.WriteString("## Pinned\n" + p + "\n")
		if len(p) > pinnedWarn {
			fmt.Fprintf(&head, "(pinned notes are %.1f KB, crowding the rest of this brief out: ask the user which rules to retire; unpin by deleting the line from ~/.fleet/pinned.log)\n", float64(len(p))/1024)
		}
		head.WriteString("\n")
	}
	left := briefBudget - head.Len()
	take := func(full, short string) string {
		if len(full) <= left || len(full) <= len(short) {
			left -= len(full)
			return full
		}
		left -= len(short)
		return short
	}

	reports := take(s.unreadReports(), s.unreadCount())
	ext := callExt(extBrief, s)
	left -= len(ext)
	agents, err := s.agentsBrief()
	if err != nil {
		return "", err
	}
	agents = take(agents, "## Agents\nnot shown here: call fleet_status for the table\n")
	rs := s.routesBrief(left)
	left -= len(rs)
	notes := s.notesBrief(pinned, left)

	return head.String() + rs + notes + reports + ext + agents, nil
}

// unreadCount is the unread section when the headlines don't fit.
func (s *Server) unreadCount() string {
	return fmt.Sprintf("## Unread reports\n%d unread: call fleet_reports for them.\n\n", len(s.F.Mail().Peek(s.box())))
}

// agentsBrief is one line per agent: name, status and its last report
// state; fleet_status has the full table.
func (s *Server) agentsBrief() (string, error) {
	as, err := s.F.Refresh()
	if err != nil {
		return "", err
	}
	if len(as) == 0 {
		return "## Agents\nno agents\n", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Agents (%d; fleet_status for the table)\n", len(as))
	for _, a := range as {
		fmt.Fprintf(&b, "%s: %s", a.Name, statusWord(a))
		if r := a.Report; r != nil {
			fmt.Fprintf(&b, ", last report %s", r.State)
			if !r.At.IsZero() {
				fmt.Fprintf(&b, " %s ago", ago(time.Since(r.At)))
			}
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

// routesBrief is fleet_brief's route index: one "- name: hint" line per
// route, else the names alone, else a pointer to fleet_route list.
func (s *Server) routesBrief(room int) string {
	rs, err := routes.Load(s.F.Home)
	if err != nil {
		return "## Routes\n" + err.Error() + "\n\n"
	}
	if len(rs) == 0 {
		return ""
	}
	const h = "## Routes (match the request here before asking where; fleet_spawn route=NAME, fleet_route show NAME)\n"
	if full := h + routes.Index(rs) + "\n"; len(full) <= room {
		return full
	}
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.Name
	}
	if short := h + strings.Join(names, ", ") + "\n(names only: fleet_route list for the hints)\n\n"; len(short) <= room {
		return short
	}
	return fmt.Sprintf("## Routes\n%d routes: call fleet_route list before asking the user where a request goes.\n\n", len(rs))
}

// notesBrief is the newest unpinned notes that fit in room, at most
// maxBriefNotes, oldest first.
func (s *Server) notesBrief(pinned []string, room int) string {
	notes := unpinned(supervisor.TailNotes(s.F.Home, maxBriefNotes+len(pinned)), pinned)
	if len(notes) > maxBriefNotes {
		notes = notes[len(notes)-maxBriefNotes:]
	}
	if len(notes) == 0 {
		return "## Notes\nnone\n\n"
	}
	const more = "(older notes: fleet_recall QUERY searches them all)\n"
	room -= len("## Notes\n") + len(more) + 1
	n, used := 0, 0
	for i := len(notes) - 1; i >= 0 && used+len(notes[i])+1 <= room; i-- {
		used += len(notes[i]) + 1
		n++
	}
	kept := notes[len(notes)-n:]
	var b strings.Builder
	b.WriteString("## Notes\n")
	if n > 0 {
		b.WriteString(strings.Join(kept, "\n") + "\n")
	}
	b.WriteString(more)
	b.WriteString("\n")
	return b.String()
}
