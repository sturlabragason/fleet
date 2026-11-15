// Package whatsnew reads fleet's own changelog (embedded CHANGELOG.md),
// adds uncovered feat commits from fleet's source repo when it runs from
// one, and remembers which entries were seen (DESIGN.md §5d, ADR-30).
package whatsnew

import (
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
)

// DateLayout is the format of a day heading and Entry.Date.
const DateLayout = "2006-01-02"

// Entry is one user-facing change.
type Entry struct {
	Date   string   `json:"date"` // YYYY-MM-DD
	Text   string   `json:"text"` // Markdown, without the ref bracket
	Refs   []string `json:"refs,omitempty"`
	Hashes []string `json:"-"`      // the commit hashes among Refs
	Commit bool     `json:"commit"` // a fallback feat commit, not a changelog line
}

// ID identifies the entry in the seen file: the first cited hash, else a
// hash of date and text, so editing a line marks it new again.
func (e Entry) ID() string {
	if len(e.Hashes) > 0 {
		return e.Hashes[0]
	}
	s := sha1.Sum([]byte(e.Date + "\x00" + e.Text))
	return hex.EncodeToString(s[:6])
}

var (
	dayRe  = regexp.MustCompile(`^##\s+(\d{4}-\d{2}-\d{2})\s*$`)
	hashRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	adrRe  = regexp.MustCompile(`^ADR-\d+$`)
)

// Parse reads `## YYYY-MM-DD` sections of `- text` bullets. Lines outside
// a dated section are ignored. A trailing bracket of commit hashes and
// ADR-n becomes Refs; any other bracket stays in the text.
func Parse(md string) []Entry {
	var out []Entry
	day := ""
	for l := range strings.SplitSeq(md, "\n") {
		l = strings.TrimRight(l, " \t\r")
		if strings.HasPrefix(l, "#") {
			day = ""
			if m := dayRe.FindStringSubmatch(l); m != nil {
				if _, err := time.Parse(DateLayout, m[1]); err == nil {
					day = m[1]
				}
			}
			continue
		}
		if day == "" || !strings.HasPrefix(l, "- ") {
			continue
		}
		e := Entry{Date: day, Text: strings.TrimSpace(l[2:])}
		e.Text, e.Refs, e.Hashes = splitRefs(e.Text)
		if e.Text != "" {
			out = append(out, e)
		}
	}
	return out
}

// splitRefs cuts a trailing "(ref, ref)" off text when every item is a
// hash or ADR-n.
func splitRefs(text string) (string, []string, []string) {
	if !strings.HasSuffix(text, ")") {
		return text, nil, nil
	}
	i := strings.LastIndex(text, "(")
	if i < 0 {
		return text, nil, nil
	}
	var refs, hashes []string
	for r := range strings.SplitSeq(text[i+1:len(text)-1], ",") {
		r = strings.TrimSpace(r)
		switch {
		case hashRe.MatchString(r):
			hashes = append(hashes, r)
		case adrRe.MatchString(r):
		default:
			return text, nil, nil
		}
		refs = append(refs, r)
	}
	return strings.TrimSpace(text[:i]), refs, hashes
}

// Plain is text with Markdown backticks removed.
func Plain(text string) string { return strings.ReplaceAll(text, "`", "") }

// List is the changelog of md plus, when repo is set, its uncovered feat
// commits: days newest first, a day's commits after its curated lines.
func List(md, repo string) []Entry {
	curated := Parse(md)
	if repo == "" {
		return curated
	}
	commits, err := Commits(repo, curated)
	if err != nil {
		logx.For("whatsnew").Warn("commit fallback", "repo", repo, "err", err)
	}
	return merge(curated, commits)
}

func merge(curated, commits []Entry) []Entry {
	if len(commits) == 0 {
		return curated
	}
	byDay := map[string][]Entry{}
	var days []string
	add := func(e Entry) {
		if _, ok := byDay[e.Date]; !ok {
			days = append(days, e.Date)
		}
		byDay[e.Date] = append(byDay[e.Date], e)
	}
	for _, e := range curated {
		add(e)
	}
	for _, e := range commits {
		add(e)
	}
	sort.SliceStable(days, func(i, j int) bool { return days[i] > days[j] })
	out := make([]Entry, 0, len(curated)+len(commits))
	for _, d := range days {
		out = append(out, byDay[d]...)
	}
	return out
}
