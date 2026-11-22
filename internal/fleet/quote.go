package fleet

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/spend"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/todo"
	"github.com/sturlabragason/fleet/internal/tools"
)

// The Alt-r quote picker (ADR-189): an earlier admiral reply, open item or
// report goes into the admiral's input box as a "> quote" under a [re …]
// header, so the admiral reads the exact text the user answers.

// QuoteLines is how many lines of the original a quote keeps.
const QuoteLines = 8

// QuoteMax is how many replies the picker offers, newest first.
const QuoteMax = 20

// Quotable is one thing the picker can quote.
type Quotable struct {
	Ref  string    // what the header names: "#3", "todo b5ea11fd", "report … from a1"
	T    time.Time // zero when unknown (the screen)
	Text string
}

// Quote is the text pasted into the input box: the [re …] header, then at
// most QuoteLines lines of the original, cut with "…", then an empty line
// for the user's answer.
func (q Quotable) Quote() string {
	h := "[re " + q.Ref
	if !q.T.IsZero() {
		h += ", " + q.T.Local().Format("15:04")
	}
	var ls []string
	for l := range strings.SplitSeq(strings.TrimSpace(q.Text), "\n") {
		if l = strings.TrimRight(l, " \t"); l == "" && (len(ls) == 0 || ls[len(ls)-1] == ">") {
			continue
		}
		if len(ls) == QuoteLines {
			ls = append(ls, "> …")
			break
		}
		ls = append(ls, strings.TrimRight("> "+l, " "))
	}
	return h + "]\n" + strings.Join(ls, "\n") + "\n\n"
}

// LogReplies reads a session's main-agent replies, oldest first, from the
// tool's log: the profile's subagent_log (Copilot events.jsonl) or, for
// Claude patterns, the session .jsonl. ok is false when the profile keeps no
// log fleet reads or it cannot be read.
func LogReplies(p tools.Profile, sessionID string) ([]status.Reply, bool) {
	var rs []status.Reply
	var err error
	if path := p.SubagentLogPath(sessionID); path != "" {
		rs, err = status.Replies(path)
	} else if path, kind := spend.Source(p, sessionID); kind == spend.Claude {
		rs, err = status.ClaudeReplies(path)
	} else {
		return nil, false
	}
	return rs, err == nil
}

// AdmiralReplies is the admiral's last QuoteMax replies, newest first, each
// numbered by its place from the newest (#1). A tool with no log fleet reads
// falls back to the visible screen, split into its blank-line blocks;
// fromScreen says so.
func (f *Fleet) AdmiralReplies(adm string) (qs []Quotable, fromScreen bool, err error) {
	s := supervisor.CurrentOf(f.Home, f.T, Session+":main", adm)
	rs, ok := LogReplies(f.Profiles[s.Profile], s.SessionID)
	if !ok {
		if s.PaneID == "" {
			return nil, true, fmt.Errorf("admiral %s has no pane", adm)
		}
		screen, err := f.T.Capture(s.PaneID, 0)
		if err != nil {
			return nil, true, fmt.Errorf("read admiral screen: %w", err)
		}
		for b := range strings.SplitSeq(status.CleanScreen(screen), "\n\n") {
			if strings.TrimSpace(b) != "" {
				rs = append(rs, status.Reply{Text: b})
			}
		}
		fromScreen = true
	}
	for i := len(rs) - 1; i >= 0 && len(qs) < QuoteMax; i-- {
		qs = append(qs, Quotable{Ref: fmt.Sprintf("#%d", len(qs)+1), T: rs[i].T, Text: rs[i].Text})
	}
	return qs, fromScreen, nil
}

// minRef is the shortest id the resolver looks up, so a filter word does
// not match every id.
const minRef = 4

// ResolveRef finds what an id the user typed names, read-only: the
// admiral's open items whose id starts with ref (todo store, ADR-164) and
// the reports in its mailbox whose id starts with ref. None when ref is
// shorter than minRef or holds a space.
func (f *Fleet) ResolveRef(adm, ref string) []Quotable {
	ref = strings.TrimSpace(ref)
	if len(ref) < minRef || strings.ContainsAny(ref, " \t") {
		return nil
	}
	var out []Quotable
	_, _ = TodoDo(func(c context.Context, st todo.Store) error { // no store or a failed read: nothing to quote
		its, err := st.Admiral(c, adm)
		for _, it := range its {
			if strings.HasPrefix(it.ID(), ref) {
				out = append(out, Quotable{Ref: "todo " + it.ID(), T: it.Entry, Text: it.Kind + ": " + it.Description})
			}
		}
		return err
	})
	for _, m := range slices.Backward(f.Mail().Box(mail.BoxOf(adm))) {
		if m.Kind == mail.KindReport && strings.HasPrefix(m.ID, ref) {
			out = append(out, Quotable{Ref: "report " + m.ID + " from " + m.From, T: m.T, Text: m.Text})
		}
	}
	return out
}
