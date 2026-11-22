// Package page turns a brief, the JSON an agent writes for a page the user
// reads, into one self-contained HTML page in the executive format of
// ADR-197: the answer first, a confidence label, the thread from evidence to
// recommendation, decision cards with a recommended pick and a free-text
// override, sources a hover away, and the full analysis folded below. Check
// refuses a brief that would read as a wall of text; Write renders it with
// the answer.json the artifact host needs to take a Send (ADR-163).
package page

import (
	"encoding/json"
	"fmt"
	"os"
)

// Brief is the whole page. Text fields are plain text: a blank line starts a
// paragraph, `code` is code and [n] cites source n.
type Brief struct {
	Page       string      `json:"page"`  // artifact name; decide- or viz- when it has decisions
	Kind       string      `json:"kind"`  // decision, status, report or research
	Title      string      `json:"title"` // the answer, as a sentence
	Date       string      `json:"date"`
	Author     string      `json:"author"`
	Round      int         `json:"round,omitempty"` // bump to ask again; Send counts once per round
	Summary    Summary     `json:"summary"`
	Confidence *Confidence `json:"confidence,omitempty"`
	KPIs       []KPI       `json:"kpis,omitempty"`
	Thread     []Step      `json:"thread,omitempty"`
	Sections   []Section   `json:"sections,omitempty"`
	Status     []StatusRow `json:"status,omitempty"`
	Decisions  []Decision  `json:"decisions,omitempty"`
	Defaults   []string    `json:"defaults,omitempty"`
	Sources    []Source    `json:"sources,omitempty"`
	Details    []Detail    `json:"details,omitempty"`
}

// Summary is the bottom line: the answer, up to five points, and the ask.
type Summary struct {
	Answer string   `json:"answer"`
	Points []string `json:"points,omitempty"`
	Ask    string   `json:"ask,omitempty"`
}

// Confidence is high, medium or low, with what it rests on (ICD 203).
type Confidence struct {
	Level string `json:"level"`
	Basis string `json:"basis"`
}

// KPI is one figure that matters; State is ok, warn or bad.
type KPI struct {
	Value string `json:"value"`
	Label string `json:"label"`
	State string `json:"state,omitempty"`
}

// Step is one link in the thread from evidence to recommendation.
type Step struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

// Section is one finding: a heading that states it, short text, and a
// chart or a table when one says it better.
type Section struct {
	Heading string `json:"heading"`
	Text    string `json:"text,omitempty"`
	Bars    *Bars  `json:"bars,omitempty"`
	Table   *Table `json:"table,omitempty"`
}

// Bars is a horizontal bar chart.
type Bars struct {
	Unit  string `json:"unit,omitempty"`
	Items []Bar  `json:"items"`
}

// Bar is one bar; State colours it (ok, warn, bad), empty is the accent.
type Bar struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
	State string  `json:"state,omitempty"`
}

// Table is for what a picture can't say better.
type Table struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

// StatusRow is one line of a status board.
type StatusRow struct {
	Item  string `json:"item"`
	State string `json:"state"`
	Note  string `json:"note,omitempty"`
	Next  string `json:"next,omitempty"`
}

// Decision is one card: a question, options with their consequences and the
// recommended one preselected. The user may write a free-text answer, which
// overrides the pick.
type Decision struct {
	ID        string   `json:"id"`
	Group     string   `json:"group,omitempty"`
	Question  string   `json:"question"`
	Context   string   `json:"context,omitempty"`
	Options   []Option `json:"options"`
	Recommend string   `json:"recommend,omitempty"`
	Why       string   `json:"why,omitempty"`
}

// Option is one answer to a decision.
type Option struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Consequence string `json:"consequence,omitempty"`
	Cost        string `json:"cost,omitempty"`
	Risk        string `json:"risk,omitempty"`
}

// Source is one cited source: checkable from its quote without opening it.
type Source struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
	Quote string `json:"quote,omitempty"`
	Date  string `json:"date,omitempty"`
}

// Detail is the full analysis behind the page, folded: nothing is dropped.
type Detail struct {
	Heading string `json:"heading"`
	Text    string `json:"text"`
}

// Load reads a brief, refusing unknown fields so a typo is not a silent gap.
func Load(path string) (Brief, error) {
	f, err := os.Open(path)
	if err != nil {
		return Brief{}, fmt.Errorf("open brief: %w", err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var b Brief
	if err := dec.Decode(&b); err != nil {
		return Brief{}, fmt.Errorf("read brief %s: %w", path, err)
	}
	return b, nil
}

// DecisionID is the id of this round of questions, which Send counts once.
func (b Brief) DecisionID() string {
	return fmt.Sprintf("%s-%d", b.Page, max(b.Round, 1))
}
