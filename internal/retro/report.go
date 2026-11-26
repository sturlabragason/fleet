package retro

import (
	"sort"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/recall"
	"github.com/sturlabragason/fleet/internal/runs"
)

// Verdict states.
const (
	Pending  = "pending"   // an arm has fewer than MinArmRuns runs
	Worse    = "worse"     // an interval puts the variant below control
	NotWorse = "not_worse" // no interval separates the variant as worse
)

// MinArmRuns is how many closed runs each arm needs before a verdict: the
// ledger's own "few" threshold (runs.MinRuns).
const MinArmRuns = runs.MinRuns

// Verdict compares a trial's arms on done rate, rework and runs with an
// intervention, each a 95% Wilson interval.
type Verdict struct {
	Trial   string   `json:"trial"`
	Control runs.Row `json:"control"`
	Variant runs.Row `json:"variant"`
	State   string   `json:"state"`
	Worse   []string `json:"worse,omitempty"` // the measures that separate it
}

// Judge splits the runs of trial t by arm and gives the verdict.
func Judge(t Trial, rs []runs.Record) Verdict {
	v := Verdict{Trial: t.ID, Control: runs.Row{Key: Control}, Variant: runs.Row{Key: Variant}}
	prefix := t.ID + "/"
	for _, row := range runs.Summarize(rs, func(r runs.Record) string {
		if r.Config == nil {
			return ""
		}
		arm, ok := strings.CutPrefix(r.Config.Arm, prefix)
		if !ok {
			return ""
		}
		return arm
	}) {
		switch row.Key {
		case Control:
			v.Control = row
		case Variant:
			v.Variant = row
		}
	}
	c, x := v.Control, v.Variant
	if c.Runs < MinArmRuns || x.Runs < MinArmRuns {
		v.State = Pending
		return v
	}
	if below(x.Done, x.Runs, c.Done, c.Runs) {
		v.Worse = append(v.Worse, "done rate")
	}
	if below(c.Rework, c.Runs, x.Rework, x.Runs) {
		v.Worse = append(v.Worse, "rework")
	}
	if below(c.Intervened, c.Runs, x.Intervened, x.Runs) {
		v.Worse = append(v.Worse, "interventions")
	}
	v.State = NotWorse
	if len(v.Worse) > 0 {
		v.State = Worse
	}
	return v
}

// below is whether a's interval lies wholly under b's.
func below(ak, an, bk, bn int) bool {
	_, aHi := runs.Wilson(ak, an)
	bLo, _ := runs.Wilson(bk, bn)
	return aHi < bLo
}

// Rule ties a worker playbook rule (found by Anchor, a phrase of it) to the
// intervention kinds it was written against: "kind", or "kind:detail" for
// one done_refused code.
type Rule struct {
	Anchor string   `json:"anchor"`
	Kinds  []string `json:"kinds"`
	Fired  int      `json:"fired"`
}

// Rules are the playbook rules the ledger can see. A rule none of whose
// kinds fired in a window with enough runs is a deletion candidate.
var Rules = []Rule{
	{Anchor: "Escalate early", Kinds: []string{board.KindStuckWake}},
	{Anchor: "Report with `fleet_report`", Kinds: []string{board.KindStopBlock, board.KindNoReportWake}},
	{Anchor: "Done means committed and merged", Kinds: []string{board.KindDoneRefused + ":uncommitted", board.KindDoneRefused + ":unmerged"}},
	{Anchor: "Queue: treat your work as a todo queue", Kinds: []string{board.KindDoneRefused + ":open_queue"}},
}

// Theme is a word that recurs across retro: lines, with one example.
type Theme struct {
	Word    string `json:"word"`
	Lines   int    `json:"lines"`
	Example string `json:"example"`
}

// MinTheme is how many retro lines a word needs to be a theme.
const MinTheme = 3

// Report is what `fleet retro` prints.
type Report struct {
	Since      time.Time  `json:"since"`
	Runs       int        `json:"runs"` // non-interactive runs in the window
	Playbooks  []runs.Row `json:"playbooks"`
	Trial      *Trial     `json:"trial,omitempty"`
	Verdict    *Verdict   `json:"verdict,omitempty"`
	Rules      []Rule     `json:"rules"`
	RetroLines int        `json:"retro_lines"`
	Themes     []Theme    `json:"themes"`
}

// Candidates are the rules with no firing in a window of at least
// MinArmRuns runs.
func (r Report) Candidates() []Rule {
	var out []Rule
	for _, x := range r.Rules {
		if r.candidate(x) {
			out = append(out, x)
		}
	}
	return out
}

func (r Report) candidate(x Rule) bool { return x.Fired == 0 && r.Runs >= MinArmRuns }

// Build reads the ledger, events and reports in home since the given time.
func Build(home string, since time.Time) (Report, error) {
	rep := Report{Since: since}
	rs, err := runs.Read(home, since)
	if err != nil {
		return rep, err
	}
	for _, r := range rs {
		if !r.Interactive {
			rep.Runs++
		}
	}
	rep.Playbooks = runs.Summarize(rs, runs.Keys["playbook"])
	if t, ok, err := Load(home); err != nil {
		return rep, err
	} else if ok {
		all, err := runs.Read(home, t.Started)
		if err != nil {
			return rep, err
		}
		v := Judge(t, all)
		rep.Trial, rep.Verdict = &t, &v
	}
	evs, err := board.NewStore(home).ReadEvents(since)
	if err != nil {
		return rep, err
	}
	rep.Rules = countRules(evs)
	lines := retroLines(home, since)
	rep.RetroLines, rep.Themes = len(lines), themes(lines)
	return rep, nil
}

func countRules(evs []board.Event) []Rule {
	out := make([]Rule, len(Rules))
	for i, r := range Rules {
		out[i] = Rule{Anchor: r.Anchor, Kinds: r.Kinds}
		for _, e := range evs {
			for _, k := range r.Kinds {
				if k == e.Kind || k == e.Kind+":"+e.Detail {
					out[i].Fired++
				}
			}
		}
	}
	return out
}

// retroLines are the retro: lines of the reports in every mailbox under
// home since the given time. An unreadable box is skipped: this is a report.
func retroLines(home string, since time.Time) []string {
	var out []string
	for _, m := range recall.Reports(home) {
		if !m.T.Before(since) {
			out = append(out, mail.RetroLines(m.Text)...)
		}
	}
	return out
}

// stop are common words that are never a theme.
var stop = map[string]bool{
	"about": true, "after": true, "again": true, "agent": true, "because": true, "before": true,
	"being": true, "caught": true, "cost": true, "could": true, "didn": true, "does": true, "doesn": true, "from": true,
	"fleet": true, "first": true, "have": true, "into": true, "just": true, "minutes": true, "more": true, "much": true, "needed": true, "none": true,
	"only": true, "over": true, "same": true, "some": true, "than": true, "that": true,
	"then": true, "there": true, "they": true, "this": true, "took": true, "were": true,
	"what": true, "when": true, "which": true, "while": true, "with": true, "would": true,
}

// themes counts, for each word of four letters or more, the retro lines
// that use it; words in MinTheme lines or more come back, most first, at
// most ten.
func themes(lines []string) []Theme {
	count, example := map[string]int{}, map[string]string{}
	for _, l := range lines {
		seen := map[string]bool{}
		for w := range strings.FieldsFuncSeq(strings.ToLower(l), func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_'
		}) {
			w = strings.Trim(w, "-_")
			if len(w) < 4 || stop[w] || seen[w] {
				continue
			}
			seen[w] = true
			count[w]++
			if example[w] == "" {
				example[w] = clip(l, 100)
			}
		}
	}
	var out []Theme
	for w, n := range count {
		if n >= MinTheme {
			out = append(out, Theme{Word: w, Lines: n, Example: example[w]})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lines != out[j].Lines {
			return out[i].Lines > out[j].Lines
		}
		return out[i].Word < out[j].Word
	})
	return out[:min(len(out), 10)]
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
