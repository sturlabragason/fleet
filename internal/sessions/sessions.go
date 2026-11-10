// Package sessions finds sessions in the tools' own stores (Copilot
// session-state folders, Claude project transcripts) via the profiles'
// session_path globs, so `fleet adopt` works with words instead of IDs.
package sessions

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sturlabragason/fleet/internal/tools"
)

// Session is one session found in a tool's store.
type Session struct {
	ID        string    `json:"id"`
	Store     string    `json:"store"`   // the session_path pattern it came from
	Profile   string    `json:"profile"` // suggested profile to resume it with
	Path      string    `json:"path"`    // session folder or transcript file
	Dir       string    `json:"dir,omitempty"`
	Branch    string    `json:"branch,omitempty"`
	Title     string    `json:"title,omitempty"`
	Model     string    `json:"model,omitempty"`
	Updated   time.Time `json:"updated"`
	HasPrompt bool      `json:"has_prompt"`
	InUse     bool      `json:"in_use"`
	PID       int       `json:"pid,omitempty"`
	OnBoard   bool      `json:"on_board"`
}

// Opts filter List. The zero value lists everything with a user prompt.
type Opts struct {
	Words    []string        // every word must appear in title, dir, branch or ID
	All      bool            // include sessions with no prompt and ones on the board
	Limit    int             // 0 = no limit
	Board    map[string]bool // session IDs already on the board
	SkipBusy bool            // leave out sessions in use (adopt)
	Store    string          // only this session_path pattern ("" = all)
}

// IDRe is what a session ID looks like: 8+ chars so process scans stay specific.
var IDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{7,}$`)

// maxRead bounds how much of one transcript is read.
const maxRead = 256 << 10

type source struct {
	pattern  string
	profiles []tools.Profile // sorted by name
}

type cand struct {
	src      *source
	id, path string
	mtime    time.Time
}

// List returns sessions newest first. Only as many are parsed as the filters
// and Limit need.
func List(profiles map[string]tools.Profile, o Opts) ([]Session, error) {
	words := make([]string, 0, len(o.Words))
	for _, w := range o.Words {
		if w = strings.ToLower(strings.TrimSpace(w)); w != "" {
			words = append(words, w)
		}
	}
	var procs []Proc
	scanned := false
	var out []Session
	for _, c := range candidates(profiles, o.Store) {
		s := parse(c)
		s.OnBoard = o.Board[s.ID]
		if !o.All && (!s.HasPrompt || s.OnBoard) {
			continue
		}
		if !s.matches(words) {
			continue
		}
		if !scanned {
			procs, scanned = Procs(), true
		}
		if pid, _ := Holder(s.ID, []string{s.Path}, procs); pid != 0 {
			s.InUse, s.PID = true, pid
		}
		if o.SkipBusy && s.InUse {
			continue
		}
		out = append(out, s)
		if o.Limit > 0 && len(out) >= o.Limit {
			break
		}
	}
	return out, nil
}

// Lookup returns the session with exactly this ID, or else every session whose
// ID starts with it when it is at least 8 characters long.
func Lookup(profiles map[string]tools.Profile, id, store string) []Session {
	if id == "" {
		return nil
	}
	var pre []cand
	for _, c := range candidates(profiles, store) {
		if c.id == id {
			return []Session{parse(c)}
		}
		if len(id) >= 8 && strings.HasPrefix(c.id, id) {
			pre = append(pre, c)
		}
	}
	out := make([]Session, len(pre))
	for i, c := range pre {
		out[i] = parse(c)
	}
	return out
}

func (s Session) matches(words []string) bool {
	if len(words) == 0 {
		return true
	}
	hay := strings.ToLower(s.Title + "\n" + s.Dir + "\n" + s.Branch + "\n" + s.ID)
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// sources groups profiles by session_path; several profiles can share a store.
func sources(profiles map[string]tools.Profile) []*source {
	by := map[string]*source{}
	for _, n := range tools.Names(profiles) {
		p := profiles[n]
		if !strings.Contains(p.SessionPath, "{id}") {
			continue
		}
		if by[p.SessionPath] == nil {
			by[p.SessionPath] = &source{pattern: p.SessionPath}
		}
		by[p.SessionPath].profiles = append(by[p.SessionPath].profiles, p)
	}
	out := make([]*source, 0, len(by))
	for _, s := range by {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pattern < out[j].pattern })
	return out
}

// candidates globs every store and stats each match, newest first; nothing is parsed.
func candidates(profiles map[string]tools.Profile, store string) []cand {
	var out []cand
	for _, src := range sources(profiles) {
		if store != "" && src.pattern != store {
			continue
		}
		pre, suf, _ := strings.Cut(filepath.Base(src.pattern), "{id}")
		matches, _ := filepath.Glob(src.profiles[0].SessionGlob("*"))
		for _, m := range matches {
			b := filepath.Base(m)
			if !strings.HasPrefix(b, pre) || !strings.HasSuffix(b, suf) || len(b) <= len(pre)+len(suf) {
				continue
			}
			id := b[len(pre) : len(b)-len(suf)]
			if strings.HasPrefix(b, ".") || !IDRe.MatchString(id) {
				continue // e.g. Copilot's .session-operation-locks
			}
			if suf == "" { // folder-per-session store (Copilot)
				if st, err := os.Stat(m); err != nil || !st.IsDir() {
					continue
				}
			}
			out = append(out, cand{src: src, id: id, path: m, mtime: mtime(m)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].mtime.After(out[j].mtime) })
	return out
}

// mtime of a transcript, or of a session folder's events/workspace file.
func mtime(p string) time.Time {
	st, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	if st.IsDir() {
		for _, f := range []string{"events.jsonl", "workspace.yaml"} {
			if fs, err := os.Stat(filepath.Join(p, f)); err == nil {
				return fs.ModTime()
			}
		}
	}
	return st.ModTime()
}

func parse(c cand) Session {
	s := Session{ID: c.id, Store: c.src.pattern, Path: c.path, Updated: c.mtime}
	var prompt string
	if st, err := os.Stat(c.path); err == nil && st.IsDir() {
		prompt = parseCopilot(&s)
	} else {
		prompt = parseClaude(&s)
	}
	s.HasPrompt = prompt != ""
	if s.Title == "" {
		s.Title = prompt
	}
	s.Title = OneLine(s.Title, 200)
	s.Profile = suggest(c.src.profiles, s.Model)
	return s
}

// lines calls fn for each line in the first maxRead bytes of path until fn returns false.
func lines(path string, fn func(string) bool) {
	fh, err := os.Open(path)
	if err != nil {
		return
	}
	defer fh.Close()
	sc := bufio.NewScanner(io.LimitReader(fh, maxRead))
	sc.Buffer(make([]byte, 64<<10), maxRead+1)
	for sc.Scan() {
		if !fn(sc.Text()) {
			return
		}
	}
}

// parseCopilot reads workspace.yaml and the start of events.jsonl; returns the first prompt.
func parseCopilot(s *Session) string {
	var name, summary string
	lines(filepath.Join(s.Path, "workspace.yaml"), func(l string) bool {
		k, v, ok := strings.Cut(l, ":")
		if !ok || strings.HasPrefix(l, " ") {
			return true
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if v == "|" || v == ">" {
			v = ""
		}
		switch k {
		case "cwd":
			s.Dir = v
		case "branch":
			s.Branch = v
		case "name":
			name = v
		case "summary":
			summary = v
		}
		return true
	})
	s.Title = name
	if s.Title == "" {
		s.Title = summary
	}
	var prompt string
	lines(filepath.Join(s.Path, "events.jsonl"), func(l string) bool {
		start := strings.Contains(l, `"session.start"`)
		if !start && !strings.Contains(l, `"user.message"`) {
			return true
		}
		var e struct {
			Type string
			Data struct {
				SelectedModel string
				Content       any
				Context       struct{ Cwd, Branch string }
			}
		}
		if json.Unmarshal([]byte(l), &e) != nil {
			return true
		}
		switch e.Type {
		case "session.start":
			s.Model = e.Data.SelectedModel
			if s.Dir == "" {
				s.Dir = e.Data.Context.Cwd
			}
			if s.Branch == "" {
				s.Branch = e.Data.Context.Branch
			}
		case "user.message":
			if c, ok := e.Data.Content.(string); ok && strings.TrimSpace(c) != "" {
				prompt = c
			}
		}
		return prompt == "" || s.Model == ""
	})
	return prompt
}

// parseClaude reads the start of a Claude transcript; returns the first prompt.
func parseClaude(s *Session) string {
	var custom, summary, prompt string
	lines(s.Path, func(l string) bool {
		var e struct {
			Type, CustomTitle, Summary, Cwd, GitBranch string
			IsMeta                                     bool
			Message                                    struct {
				Model   string
				Content json.RawMessage
			}
		}
		if json.Unmarshal([]byte(l), &e) != nil {
			return true
		}
		switch e.Type {
		case "custom-title":
			if e.CustomTitle != "" {
				custom = e.CustomTitle
			}
		case "summary":
			if summary == "" {
				summary = e.Summary
			}
		case "assistant":
			if s.Model == "" && e.Message.Model != "" && !strings.HasPrefix(e.Message.Model, "<") {
				s.Model = e.Message.Model
			}
		case "user":
			if s.Dir == "" {
				s.Dir = e.Cwd
			}
			if s.Branch == "" {
				s.Branch = e.GitBranch
			}
			if prompt == "" && !e.IsMeta {
				prompt = userText(e.Message.Content)
			}
		}
		return true
	})
	switch {
	case custom != "":
		s.Title = custom
	case summary != "":
		s.Title = summary
	}
	return prompt
}

// userText is the text of a user message (string or [{type:text}]); empty for
// tool results and wrapped command/caveat messages.
func userText(raw json.RawMessage) string {
	var str string
	if json.Unmarshal(raw, &str) != nil {
		var parts []struct{ Type, Text string }
		if json.Unmarshal(raw, &parts) != nil {
			return ""
		}
		for _, p := range parts {
			if p.Type == "text" && strings.TrimSpace(p.Text) != "" {
				str = p.Text
				break
			}
		}
	}
	str = strings.TrimSpace(str)
	if strings.HasPrefix(str, "<") {
		return ""
	}
	return str
}

// suggest picks the profile whose --model shares the session model's family,
// else the one with no args, else the first.
func suggest(ps []tools.Profile, model string) string {
	if fam := family(model); fam != "" {
		for _, p := range ps {
			if m := modelArg(p.Args); m != "" && family(m) == fam {
				return p.Name
			}
		}
	}
	for _, p := range ps {
		if len(p.Args) == 0 {
			return p.Name
		}
	}
	return ps[0].Name
}

func family(model string) string {
	f, _, _ := strings.Cut(strings.ToLower(model), "-")
	return f
}

func modelArg(args []string) string {
	for i, a := range args {
		if a == "--model" && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, "--model="); ok {
			return v
		}
	}
	return ""
}

// OneLine collapses whitespace and cuts to n runes with an ellipsis.
func OneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// Age is a compact age: 5m, 3h, 2d.
func Age(d time.Duration) string {
	switch {
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + "d"
}

// Short is the first 8 characters of an ID.
func Short(id string) string { return id[:min(8, len(id))] }

// Newest is the most recently written session in p's store that started in
// dir, has a user prompt and was written after since: the session a tool
// running in dir switched to after /clear (ADR-101). ok is false when none.
// A cheap dir check (Claude's project folder name, Copilot's workspace.yaml
// cwd) runs before any transcript is parsed.
func Newest(p tools.Profile, dir string, since time.Time) (Session, bool) {
	if p.SessionPath == "" || dir == "" {
		return Session{}, false
	}
	dir = filepath.Clean(dir)
	proj := claudeProject(dir)
	for _, c := range candidates(map[string]tools.Profile{p.Name: p}, p.SessionPath) {
		if !c.mtime.After(since) {
			break
		}
		if st, err := os.Stat(c.path); err == nil && st.IsDir() {
			if filepath.Clean(workspaceCwd(c.path)) != dir {
				continue
			}
		} else if filepath.Base(filepath.Dir(c.path)) != proj {
			continue
		}
		if s := parse(c); s.HasPrompt && s.Dir != "" && filepath.Clean(s.Dir) == dir {
			return s, true
		}
	}
	return Session{}, false
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// claudeProject is the folder Claude keeps a dir's transcripts in.
func claudeProject(dir string) string { return nonAlnum.ReplaceAllString(dir, "-") }

// workspaceCwd is the cwd line of a Copilot session's workspace.yaml.
func workspaceCwd(sessionDir string) string {
	var cwd string
	lines(filepath.Join(sessionDir, "workspace.yaml"), func(l string) bool {
		if v, ok := strings.CutPrefix(l, "cwd:"); ok {
			cwd = strings.Trim(strings.TrimSpace(v), `"'`)
			return false
		}
		return true
	})
	return cwd
}
