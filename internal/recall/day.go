package recall

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Agent is what the day digest needs of a board agent.
type Agent struct {
	Name, Status, Dir string
	ReportState       string // the latest report's state, "" when none
	ReportHead        string
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
// the sessions run outside fleet.
func Day(ctx context.Context, o DayOpts) string {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	dirs := map[string]string{}
	for k, v := range o.Dirs {
		dirs[k] = v
	}
	sessions, cassErr := cassSessions(ctx, o.Since)
	for _, s := range sessions {
		if n := worktreeAgent(s.Dir); n != "" && dirs[n] == "" {
			dirs[n] = s.Dir
		}
	}
	var b strings.Builder

	b.WriteString("## Waiting on you\n")
	listed := map[string]bool{}
	var wait []string
	for _, m := range o.Unread {
		if st := m.State(); st == "question" || st == "blocked" {
			wait = append(wait, fmt.Sprintf("%s %s: %s", stamp(m.T, o.Now), m.From, clip(firstLine(m.Text), 160)))
			listed[m.From] = true
		}
	}
	for _, a := range o.Agents {
		switch {
		case listed[a.Name]:
		case needsYou[a.Status]:
			wait = append(wait, fmt.Sprintf("%s is %s (%s)", a.Name, a.Status, Area(a.Dir)))
		case a.ReportState == "question" || a.ReportState == "blocked":
			wait = append(wait, fmt.Sprintf("%s: %s: %s", a.Name, a.ReportState, clip(a.ReportHead, 160)))
		default:
			continue
		}
		listed[a.Name] = true
	}
	if len(o.Unread) > 0 {
		wait = append(wait, fmt.Sprintf("%d unread reports (fleet reports / fleet_reports)", len(o.Unread)))
	}
	writeList(&b, wait)

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
	if cassErr != nil {
		if cassErr == ErrNoCass {
			b.WriteString("\n" + CassHint + "\n")
		} else {
			b.WriteString("\n" + cassErr.Error() + "\n")
		}
		return b.String()
	}
	other := map[string][]entry{}
	for _, s := range sessions {
		if worktreeAgent(s.Dir) != "" || isFleetPrompt(s.Title) || s.Title == "" || s.messages < minMessages || inTemp(s.Dir) {
			continue
		}
		other[Area(s.Dir)] = append(other[Area(s.Dir)], entry{s.At, fmt.Sprintf("%s %s: %s", stamp(s.At, o.Now), s.Tool, clip(s.Title, 100))})
	}
	b.WriteString(" (cass)\n")
	writeAreas(&b, other)
	return b.String()
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

type session struct {
	Hit
	messages int
}

// cassSessions lists the sessions modified since t.
func cassSessions(ctx context.Context, since time.Time) ([]session, error) {
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
		return nil, fmt.Errorf("cass sessions: unreadable output: %v", err)
	}
	var hits []session
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
		hits = append(hits, session{Hit{Kind: "session", At: t.Local(), Tool: s.Agent, Dir: dir, Title: firstLine(s.Title), Path: s.Path}, s.Messages})
	}
	return hits, nil
}
