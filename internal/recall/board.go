package recall

import (
	"context"
	"sort"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/mail"
)

// Board is what recall and the day digest take from the board: the live
// agents, each known agent's dir, and the session id each ran under
// (live and recently closed ones).
type Board struct {
	Agents   []Agent
	Dirs     map[string]string
	Sessions map[string]string // session id -> agent name
}

// FromBoard reads b.
func FromBoard(b *board.Board) Board {
	out := Board{Dirs: map[string]string{}, Sessions: map[string]string{}}
	for _, c := range b.ClosedRecords() {
		out.Dirs[c.Agent.Name] = c.Agent.Dir
		if c.Agent.SessionID != "" {
			out.Sessions[c.Agent.SessionID] = c.Agent.Name
		}
	}
	for _, a := range b.Agents {
		out.Dirs[a.Name] = a.Dir
		if a.SessionID != "" {
			out.Sessions[a.SessionID] = a.Name
		}
		ag := Agent{Name: a.Name, Status: a.Status, Dir: a.Dir}
		if a.Report != nil {
			ag.ReportState, ag.ReportHead = a.Report.State, firstLine(a.Report.Summary)
		}
		out.Agents = append(out.Agents, ag)
	}
	sort.Slice(out.Agents, func(i, j int) bool { return out.Agents[i].Name < out.Agents[j].Name })
	return out
}

// FromMail converts mail messages.
func FromMail(ms []mail.Msg) []Msg {
	out := make([]Msg, 0, len(ms))
	for _, m := range ms {
		out = append(out, Msg{ID: m.ID, From: m.From, Kind: m.Kind, Text: m.Text, T: m.T})
	}
	return out
}

// DayFor is Day over a board and an admiral's unread reports.
func DayFor(ctx context.Context, home string, b *board.Board, unread []mail.Msg, since time.Time) string {
	bd := FromBoard(b)
	return Day(ctx, DayOpts{Home: home, Since: since, Agents: bd.Agents, Dirs: bd.Dirs, Unread: FromMail(unread)})
}
