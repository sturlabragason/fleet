package recall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
)

// Agent is what the day digest needs of a board agent.
type Agent struct {
	Name, Status, Dir string
	ReportState       string // the latest report's state, "" when none
	ReportHead        string
	ReportAt          time.Time
	Open              bool // the report is an ask nobody answered (board.Agent.OpenAsk)
}

// DayOpts feeds Day.
type DayOpts struct {
	Home       string
	Since, Now time.Time
	Agents     []Agent           // the live board
	Dirs       map[string]string // agent name -> dir, board and closed records
	Unread     []Msg             // the admiral's unread reports
}

// needsYou are the board statuses that wait on the user.
var needsYou = map[string]bool{"waiting": true, "error": true, "stuck": true}

// Day is the token-free digest (ADR-119): what waits on the user first,
// then what got done since o.Since by area, what is still running, and
// the sessions run outside fleet, then each extension's sections.
func Day(ctx context.Context, o DayOpts) string {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	dirs := map[string]string{}
	for k, v := range o.Dirs {
		dirs[k] = v
	}
	sessions, cassErr := Sessions(ctx, o.Since)
	for _, s := range sessions {
		if n := s.Agent; n != "" && dirs[n] == "" {
			dirs[n] = s.Dir
		}
	}
	var b strings.Builder

	writeWaiting(&b, o)

	fmt.Fprintf(&b, "\n## Done since %s\n", o.Since.Format("Mon 15:04"))
	last := map[string]Msg{}
	for _, m := range Reports(o.Home) {
		if m.State() == "done" && !m.T.Before(o.Since) {
			last[m.From] = m
		}
	}
	byArea := map[string][]entry{}
	for _, m := range last {
		head := strings.TrimSpace(strings.TrimPrefix(firstLine(m.Text), "done:"))
		a := Area(dirs[m.From])
		byArea[a] = append(byArea[a], entry{m.T, fmt.Sprintf("%s %s: %s", stamp(m.T, o.Now), m.From, clip(head, 160))})
	}
	writeAreas(&b, byArea)

	b.WriteString("\n## Still on the board\n")
	running := map[string][]entry{}
	for _, a := range o.Agents {
		line := a.Name + " " + a.Status
		if a.ReportHead != "" {
			line += ": " + clip(a.ReportHead, 120)
		}
		running[Area(a.Dir)] = append(running[Area(a.Dir)], entry{line: line})
	}
	writeAreas(&b, running)

	b.WriteString("\n## Sessions outside fleet")
	switch {
	case errors.Is(cassErr, ErrNoCass):
		b.WriteString("\n" + CassHint + "\n")
	case cassErr != nil:
		b.WriteString("\n" + cassErr.Error() + "\n")
	default:
		other := map[string][]entry{}
		for _, s := range sessions {
			if s.Agent != "" || isFleetPrompt(s.Title) || s.Title == "" || s.Messages < minMessages || inTemp(s.Dir) {
				continue
			}
			other[Area(s.Dir)] = append(other[Area(s.Dir)], entry{s.At, fmt.Sprintf("%s %s: %s", stamp(s.At, o.Now), s.Tool, clip(s.Title, 100))})
		}
		b.WriteString(" (cass)\n")
		writeAreas(&b, other)
	}
	b.WriteString(extDay(ctx, o))
	return b.String()
}

// fleetNotices open the reports fleet files itself in an agent's name
// (internal/fleet: stuck, no report, recycle, re-surface, crash, admiral
// exit). They are notices, not asks (ADR-130).
var fleetNotices = []string{"stuck: ", "no report: ", "recycle: ", "resurface: ", "crashed ("}

// FleetNotice reports whether body, a report's text after "state: ", is
// one of fleet's own notices.
func FleetNotice(body string) bool {
	for _, p := range fleetNotices {
		if strings.HasPrefix(body, p) {
			return true
		}
	}
	return strings.HasPrefix(body, "admiral ") && strings.Contains(body, "'s pane exited")
}

// writeWaiting is "Waiting on you" (ADR-130): every open ask, read or
// unread, oldest first with its age; then agents whose screen waits; then
// fleet's own unread notices under their own heading, not counted.
func writeWaiting(b *strings.Builder, o DayOpts) {
	type ask struct {
		at   time.Time
		line string
	}
	var asks []ask
	listed := map[string]bool{}
	for _, a := range o.Agents {
		if a.Open {
			asks = append(asks, ask{a.ReportAt, fmt.Sprintf("%s %s %s: %s", board.Age(o.Now.Sub(a.ReportAt)), a.Name, a.ReportState, clip(a.ReportHead, 160))})
			listed[a.Name] = true
		}
	}
	onBoard := map[string]bool{}
	for _, a := range o.Agents {
		onBoard[a.Name] = true
	}
	var notices []string
	for _, m := range o.Unread {
		st := m.State()
		if st != "question" && st != "blocked" {
			continue
		}
		body := strings.TrimSpace(strings.TrimPrefix(firstLine(m.Text), st+":"))
		line := fmt.Sprintf("%s %s", board.Age(o.Now.Sub(m.T)), m.From)
		switch {
		case FleetNotice(body):
			notices = append(notices, line+": "+clip(body, 160))
		case !onBoard[m.From] && !listed[m.From]:
			// A closed agent's unread ask: the board can't say it was answered.
			asks = append(asks, ask{m.T, line + " " + st + ": " + clip(body, 160)})
			listed[m.From] = true
		}
	}
	sort.SliceStable(asks, func(i, j int) bool { return asks[i].at.Before(asks[j].at) })
	b.WriteString("## Waiting on you")
	if len(asks) > 0 {
		fmt.Fprintf(b, " (%d)", len(asks))
	}
	b.WriteString("\n")
	var wait []string
	for _, a := range asks {
		wait = append(wait, a.line)
	}
	for _, a := range o.Agents {
		if !listed[a.Name] && needsYou[a.Status] {
			wait = append(wait, fmt.Sprintf("%s is %s (%s)", a.Name, a.Status, Area(a.Dir)))
		}
	}
	if len(o.Unread) > 0 {
		wait = append(wait, fmt.Sprintf("%d unread reports (fleet reports / fleet_reports)", len(o.Unread)))
	}
	writeList(b, wait)
	if len(notices) > 0 {
		fmt.Fprintf(b, "### Fleet notices (%d)\n", len(notices))
		writeList(b, notices)
	}
}

func isFleetPrompt(title string) bool {
	for _, p := range fleetPrompts {
		if strings.HasPrefix(title, p) {
			return true
		}
	}
	return false
}

func writeList(b *strings.Builder, lines []string) {
	if len(lines) == 0 {
		b.WriteString("nothing\n")
	}
	for _, l := range lines {
		b.WriteString("- " + l + "\n")
	}
}

// maxPerArea caps each area's lines; the digest stays one screen.
const maxPerArea = 8

// entry is one digest line; at orders an area's lines, newest first.
type entry struct {
	at   time.Time
	line string
}

// minMessages drops one-shot sessions (fleet's peek summaries and
// probes) from "outside fleet".
const minMessages = 4

// inTemp is true for a dir under the temp dir: tests and probes.
func inTemp(dir string) bool {
	for _, t := range []string{os.TempDir(), "/tmp"} {
		if dir == t || strings.HasPrefix(dir, strings.TrimSuffix(t, "/")+"/") {
			return true
		}
	}
	return false
}

// stamp is 15:04 for today, Mon 15:04 before that.
func stamp(t, now time.Time) string {
	if y, m, d := t.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return t.Format("15:04")
	}
	return t.Format("Mon 15:04")
}

func writeAreas(b *strings.Builder, areas map[string][]entry) {
	if len(areas) == 0 {
		b.WriteString("nothing\n")
		return
	}
	keys := make([]string, 0, len(areas))
	for k := range areas {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(areas[keys[i]]) != len(areas[keys[j]]) {
			return len(areas[keys[i]]) > len(areas[keys[j]])
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		ls := areas[k]
		sort.SliceStable(ls, func(i, j int) bool { return ls[i].at.After(ls[j].at) })
		fmt.Fprintf(b, "%s (%d)\n", k, len(ls))
		for i, l := range ls {
			if i == maxPerArea {
				fmt.Fprintf(b, "  … %d more\n", len(ls)-maxPerArea)
				break
			}
			b.WriteString("  " + l.line + "\n")
		}
	}
}

// Session is one tool session cass indexed.
type Session struct {
	At                     time.Time
	Tool, Dir, Title, Path string
	Messages               int
	// Agent is the fleet agent whose worktree Dir is, "" outside fleet.
	Agent string
}

// Sessions lists the sessions cass indexed that were modified since since;
// ErrNoCass when cass is missing or off.
func Sessions(ctx context.Context, since time.Time) ([]Session, error) {
	out, err := runCass(ctx, cassTimeout, "sessions", "--json", "--since", since.Format("2006-01-02"), "--limit", strconv.Itoa(1000))
	if err != nil {
		return nil, err
	}
	var res struct {
		Sessions []struct {
			Path      string  `json:"path"`
			Workspace *string `json:"workspace"`
			Agent     string  `json:"agent"`
			Title     string  `json:"title"`
			Modified  string  `json:"modified"`
			Messages  int     `json:"message_count"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("cass sessions: unreadable output: %w", err)
	}
	var hits []Session
	for _, s := range res.Sessions {
		t, _ := time.Parse(time.RFC3339, s.Modified)
		if t.Before(since) {
			continue
		}
		dir := ""
		if s.Workspace != nil {
			dir = *s.Workspace
		}
		if dir == "" {
			dir = copilotCwd(s.Path)
		}
		hits = append(hits, Session{At: t.Local(), Tool: s.Agent, Dir: dir, Title: firstLine(s.Title), Path: s.Path, Messages: s.Messages, Agent: worktreeAgent(dir)})
	}
	return hits, nil
}
