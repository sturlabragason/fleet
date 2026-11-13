// Package recall answers "what was I doing on X?" and "what happened
// today?" without calling a model (ADR-119). It searches fleet's own
// records (agent reports in the mail inboxes, decisions.log and
// pinned.log) and, when cass is installed, every Copilot CLI and Claude
// Code session on the machine through cass (cass.go).
package recall

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Hit is one match: a report, a note or a session.
type Hit struct {
	Kind  string    `json:"kind"` // report, note or session
	At    time.Time `json:"at"`
	Who   string    `json:"who,omitempty"`  // fleet agent, "admiral", or "" when unknown
	Tool  string    `json:"tool,omitempty"` // the session's tool, as cass names it
	Dir   string    `json:"dir,omitempty"`
	Title string    `json:"title,omitempty"`
	Text  string    `json:"text,omitempty"`
	Path  string    `json:"path,omitempty"` // the session file
	ID    string    `json:"session_id,omitempty"`
}

// Opts scopes a search.
type Opts struct {
	Home  string // fleet home (~/.fleet)
	Days  int    // only the last N days; 0 is all
	Limit int    // per section; 0 is 10
	// Agents maps a session id to the fleet agent that ran it (board and
	// closed records), to name sessions whose dir does not.
	Agents map[string]string
	Now    time.Time
}

// Result is a search's answer. CassErr is ErrNoCass when cass is missing.
type Result struct {
	Topic    string        `json:"topic"`
	Fleet    []Hit         `json:"fleet"`
	Sessions []Hit         `json:"sessions"`
	CassErr  string        `json:"cass_error,omitempty"`
	IndexAge time.Duration `json:"index_age_ns,omitempty"`
	noCass   bool
}

// Search looks topic up in fleet's records and, through cass, in the
// sessions.
func Search(ctx context.Context, topic string, o Opts) Result {
	if o.Limit <= 0 {
		o.Limit = 10
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	r := Result{Topic: topic, Fleet: searchFleet(topic, o)}
	hits, err := cassSearch(ctx, topic, o.Days, o.Limit)
	switch {
	case errors.Is(err, ErrNoCass):
		r.noCass = true
		r.CassErr = CassHint
	case err != nil:
		r.CassErr = err.Error()
	}
	for i := range hits {
		name(&hits[i], o.Agents)
	}
	r.Sessions = hits
	if err == nil {
		r.IndexAge, _ = IndexAge(ctx)
	}
	return r
}

// searchFleet matches every word of topic (case-insensitive) against the
// reports and notes, newest first.
func searchFleet(topic string, o Opts) []Hit {
	words := strings.Fields(strings.ToLower(topic))
	var since time.Time
	if o.Days > 0 {
		since = o.Now.AddDate(0, 0, -o.Days)
	}
	var hits []Hit
	for _, m := range Reports(o.Home) {
		if m.T.Before(since) || !matches(m.From+" "+m.Text, words) {
			continue
		}
		head := firstLine(m.Text)
		h := Hit{Kind: "report", At: m.T, Who: m.From, Title: head}
		if l := matchingLine(m.Text, words); l != "" && l != head {
			h.Text = l
		}
		hits = append(hits, h)
	}
	for _, n := range notes(o.Home) {
		if n.At.Before(since) || !matches(n.Text, words) {
			continue
		}
		hits = append(hits, n)
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].At.After(hits[j].At) })
	if len(hits) > o.Limit {
		hits = hits[:o.Limit]
	}
	return hits
}

// Msg is a report as the inboxes store it (mail.Msg's fields).
type Msg struct {
	ID   string    `json:"id"`
	From string    `json:"from"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
	T    time.Time `json:"t"`
}

// State is the report's state: the word before the first colon.
func (m Msg) State() string {
	s, _, _ := strings.Cut(m.Text, ":")
	return s
}

// Reports reads every report in every inbox under home/mail, oldest
// first, each once. The inboxes keep every report since the first one, so
// they are fleet's history of closed agents too (ADR-119).
func Reports(home string) []Msg {
	paths, _ := filepath.Glob(filepath.Join(home, "mail", "*", "inbox.jsonl"))
	seen := map[string]bool{}
	var out []Msg
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var m Msg
			if json.Unmarshal(sc.Bytes(), &m) != nil || m.Kind != "report" || seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			m.T = m.T.Local()
			out = append(out, m)
		}
		f.Close()
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].T.Before(out[j].T) })
	return out
}

// notes reads decisions.log and pinned.log: "2006-01-02T15:04 [agent] text".
func notes(home string) []Hit {
	var out []Hit
	for _, name := range []string{"decisions.log", "pinned.log"} {
		b, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			ts, rest, ok := strings.Cut(l, " ")
			t, err := time.ParseInLocation("2006-01-02T15:04", ts, time.Local)
			if !ok || err != nil {
				continue
			}
			h := Hit{Kind: "note", At: t, Text: rest}
			if strings.HasPrefix(rest, "[") {
				if i := strings.Index(rest, "] "); i > 0 {
					h.Who, h.Text = rest[1:i], rest[i+2:]
				}
			}
			out = append(out, h)
		}
	}
	return out
}

func matches(text string, words []string) bool {
	text = strings.ToLower(text)
	for _, w := range words {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}

// matchingLine is the first line of text that holds any of words.
func matchingLine(text string, words []string) string {
	for _, l := range strings.Split(text, "\n") {
		ll := strings.ToLower(l)
		for _, w := range words {
			if strings.Contains(ll, w) {
				return clip(strings.TrimSpace(l), 200)
			}
		}
	}
	return ""
}

// fleet's own first prompts; as a session title they say nothing.
var fleetPrompts = []string{"# Fleet worker playbook", "You are taking over as fleet admiral", "# Fleet leader playbook"}

// name fills a session hit's fleet agent: from the board's session ids,
// else from a <repo>.wt/<name> dir, else "admiral" for an admiral session.
func name(h *Hit, agents map[string]string) {
	h.ID = sessionID(h.Path)
	fleetTitle := false
	for _, p := range fleetPrompts {
		if strings.HasPrefix(h.Title, p) {
			fleetTitle = true
		}
	}
	switch {
	case agents[h.ID] != "":
		h.Who = agents[h.ID]
	case worktreeAgent(h.Dir) != "":
		h.Who = worktreeAgent(h.Dir)
	case strings.HasPrefix(h.Title, "You are taking over as fleet admiral"):
		h.Who = "admiral"
	}
	if fleetTitle {
		h.Title = ""
	}
}

// worktreeAgent is <name> for a dir in <repo>.wt/<name>.
func worktreeAgent(dir string) string {
	_, rest, ok := strings.Cut(filepath.ToSlash(dir), ".wt/")
	if !ok {
		return ""
	}
	n, _, _ := strings.Cut(rest, "/")
	return n
}

// Area is the repo a dir belongs to: <repo> for <repo>.wt/<name>/..., the
// dir's base name otherwise.
func Area(dir string) string {
	if dir == "" {
		return "other"
	}
	d := filepath.ToSlash(dir)
	if i := strings.Index(d, ".wt/"); i >= 0 {
		return filepath.Base(d[:i])
	}
	if home, _ := os.UserHomeDir(); home != "" && filepath.Clean(dir) == filepath.Clean(home) {
		return "~"
	}
	return filepath.Base(d)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return clip(strings.TrimSpace(s), 200)
}

func oneLine(s string) string { return clip(strings.Join(strings.Fields(s), " "), 240) }

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// tilde shortens a path under $HOME.
func tilde(p string) string {
	if home, _ := os.UserHomeDir(); home != "" && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

// Format renders r as text for the terminal and the admiral.
func (r Result) Format() string {
	var b strings.Builder
	fmt.Fprintf(&b, "## fleet reports and notes: %d\n", len(r.Fleet))
	if len(r.Fleet) == 0 {
		b.WriteString("none\n")
	}
	for _, h := range r.Fleet {
		who := h.Who
		if who == "" {
			who = "-"
		}
		line := h.Title
		if h.Kind == "note" {
			line = h.Text
		}
		fmt.Fprintf(&b, "%s  %-6s %s: %s\n", h.At.Format("2006-01-02 15:04"), h.Kind, who, clip(line, 200))
		if h.Kind == "report" && h.Text != "" {
			fmt.Fprintf(&b, "    … %s\n", h.Text)
		}
	}
	b.WriteString("\n## sessions")
	switch {
	case r.noCass:
		b.WriteString("\n" + r.CassErr + "\n")
		return b.String()
	case r.CassErr != "":
		b.WriteString("\nsession search failed: " + r.CassErr + "\n")
		return b.String()
	}
	fmt.Fprintf(&b, " (cass): %d", len(r.Sessions))
	if r.IndexAge > 0 {
		fmt.Fprintf(&b, ", index %s old", r.IndexAge.Round(time.Minute))
	}
	b.WriteString("\n")
	if len(r.Sessions) == 0 {
		b.WriteString("none\n")
	}
	for _, h := range r.Sessions {
		who := h.Who
		if who == "" {
			who = "-"
		}
		fmt.Fprintf(&b, "%s  %s · %s · %s · %s\n", h.At.Format("2006-01-02 15:04"), who, Area(h.Dir), h.Tool, shortID(h.ID))
		if h.Title != "" {
			fmt.Fprintf(&b, "    %s\n", clip(h.Title, 120))
		}
		fmt.Fprintf(&b, "    … %s\n", h.Text)
	}
	if len(r.Sessions) > 0 {
		b.WriteString("Resume one: fleet adopt <session id>. Read it: cass view <path> (fleet recall --json has the paths).\n")
	}
	if r.IndexAge > 6*time.Hour {
		b.WriteString("The index is old: fleet recall --refresh updates it first (about 20 s).\n")
	}
	return b.String()
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
