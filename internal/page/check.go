package page

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Word limits outside the folded details. An executive scans; anything
// longer goes into Details, where it is kept whole (ADR-197).
const (
	maxAnswer   = 45
	maxPoint    = 30
	maxPoints   = 5
	maxSection  = 120
	maxStep     = 25
	maxQuestion = 30
	maxContext  = 60
	maxOption   = 30
	maxKPIs     = 4
	// maxKeys is the artifact host's cap on answer keys a page takes
	// (SCHEMA_KEYS_MAX): the cards plus the defaults list.
	maxKeys = 16
)

// Later is the option a card without a recommendation gets, preselected, so
// Submit always has a value for it.
const Later = "later"

var (
	nameRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	keyRe    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	valueRe  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	citeRe   = regexp.MustCompile(`\[(\d+)\]`)
	kinds    = map[string]bool{"decision": true, "status": true, "report": true, "research": true}
	levels   = map[string]bool{"high": true, "medium": true, "low": true}
	states   = map[string]bool{"ok": true, "warn": true, "bad": true}
	reserved = map[string]bool{"decision": true, "defaults": true, "note": true, "token": true}
)

// Check lists every way b breaks the format; none means it may be published.
func Check(b Brief) []string {
	c := &checker{}
	c.head(b)
	c.text("summary.answer", b.Summary.Answer, maxAnswer)
	for i, p := range b.Summary.Points {
		c.text(fmt.Sprintf("summary.points[%d]", i), p, maxPoint)
	}
	c.text("summary.ask", b.Summary.Ask, maxPoint)
	for i, s := range b.Thread {
		c.need(s.Label != "", "thread[%d]: no label", i)
		c.text(fmt.Sprintf("thread[%d]", i), s.Text, maxStep)
	}
	c.sections(b.Sections)
	c.status(b.Status)
	c.decisions(b)
	c.sources(b)
	return c.bad
}

type checker struct{ bad []string }

func (c *checker) need(ok bool, format string, a ...any) {
	if !ok {
		c.bad = append(c.bad, fmt.Sprintf(format, a...))
	}
}

// text checks one field's length in words.
func (c *checker) text(field, s string, most int) {
	n := len(strings.Fields(s))
	c.need(n <= most, "%s: %d words, at most %d here; move the rest to details", field, n, most)
}

func (c *checker) head(b Brief) {
	c.need(nameRe.MatchString(b.Page), "page: %q is not an artifact name (lowercase, digits, dashes)", b.Page)
	c.need(kinds[b.Kind], "kind: %q, want decision, status, report or research", b.Kind)
	c.need(b.Round >= 0 && b.Round <= 9999, "round: %d, want 1 to 9999", b.Round)
	c.need(b.Title != "" && utf8.RuneCountInString(b.Title) <= 120, "title: state the answer in one sentence of at most 120 characters")
	c.need(b.Summary.Answer != "", "summary.answer: the bottom line is missing")
	c.need(len(b.Summary.Points) <= maxPoints, "summary.points: %d, at most %d", len(b.Summary.Points), maxPoints)
	c.need(len(b.KPIs) <= maxKPIs, "kpis: %d, at most %d: keep the ones that matter", len(b.KPIs), maxKPIs)
	c.need(len(b.Thread) == 0 || len(b.Thread) >= 2 && len(b.Thread) <= 6, "thread: %d steps, want 2 to 6", len(b.Thread))
	for i, k := range b.KPIs {
		c.need(k.State == "" || states[k.State], "kpis[%d].state: %q, want ok, warn or bad", i, k.State)
	}
	if b.Kind == "decision" || b.Kind == "research" {
		c.need(b.Confidence != nil, "confidence: a %s page says how sure it is (high, medium or low, and why)", b.Kind)
		c.need(len(b.Sources) > 0, "sources: a %s page cites what it rests on", b.Kind)
	}
	if b.Confidence != nil {
		c.need(levels[b.Confidence.Level], "confidence.level: %q, want high, medium or low", b.Confidence.Level)
		c.need(b.Confidence.Basis != "", "confidence.basis: say what the confidence rests on")
	}
	c.need(b.Kind != "decision" || len(b.Decisions) > 0, "decisions: a decision page has at least one")
	c.need(b.Kind != "status" || len(b.Status) > 0, "status: a status page has at least one row")
}

func (c *checker) sections(ss []Section) {
	for i, s := range ss {
		c.need(s.Heading != "", "sections[%d]: no heading; state the finding", i)
		c.text(fmt.Sprintf("sections[%d].text", i), s.Text, maxSection)
		if s.Bars != nil {
			c.need(len(s.Bars.Items) > 0, "sections[%d].bars: no items", i)
			for j, it := range s.Bars.Items {
				c.need(it.Value >= 0, "sections[%d].bars[%d]: negative value", i, j)
				c.need(it.State == "" || states[it.State], "sections[%d].bars[%d].state: %q", i, j, it.State)
			}
		}
		if s.Table != nil {
			for j, r := range s.Table.Rows {
				c.need(len(r) == len(s.Table.Columns), "sections[%d].table row %d: %d cells for %d columns", i, j, len(r), len(s.Table.Columns))
			}
		}
	}
}

func (c *checker) status(rows []StatusRow) {
	for i, r := range rows {
		c.need(r.Item != "", "status[%d]: no item", i)
		c.need(states[r.State], "status[%d].state: %q, want ok, warn or bad", i, r.State)
		c.text(fmt.Sprintf("status[%d].note", i), r.Note, maxOption)
		c.text(fmt.Sprintf("status[%d].next", i), r.Next, maxOption)
	}
}

func (c *checker) decisions(b Brief) {
	if len(b.Decisions) > 0 {
		c.need(strings.HasPrefix(b.Page, "decide-") || strings.HasPrefix(b.Page, "viz-"),
			"page: a page with decisions is named decide-… or viz-… so fleet hands on answers nobody took (ADR-163)")
	}
	keys := len(b.Decisions)
	if len(b.Defaults) > 0 {
		keys++
	}
	c.need(keys <= maxKeys, "decisions: %d cards, at most %d per round with defaults (the host's limit): ask the rest next round", len(b.Decisions), maxKeys-1)
	c.need(len(b.Defaults) <= 30, "defaults: %d, at most 30", len(b.Defaults))
	seen := map[string]bool{}
	for i, d := range b.Decisions {
		at := fmt.Sprintf("decisions[%d]", i)
		c.need(keyRe.MatchString(d.ID) && !reserved[d.ID], "%s.id: %q, want a short lowercase key", at, d.ID)
		c.need(!seen[d.ID], "%s.id: %q twice", at, d.ID)
		seen[d.ID] = true
		c.need(d.Question != "", "%s: no question", at)
		c.text(at+".question", d.Question, maxQuestion)
		c.text(at+".context", d.Context, maxContext)
		c.text(at+".why", d.Why, maxContext)
		c.need(len(d.Options) >= 2 && len(d.Options) <= 5, "%s: %d options, want 2 to 5", at, len(d.Options))
		ids := map[string]bool{}
		for j, o := range d.Options {
			c.need(valueRe.MatchString(o.ID) && !ids[o.ID] && o.ID != Later, "%s.options[%d].id: %q, want a unique short token, not %q", at, j, o.ID, Later)
			ids[o.ID] = true
			c.need(o.Label != "", "%s.options[%d]: no label", at, j)
			for _, f := range []string{o.Consequence, o.Cost, o.Risk} {
				c.text(fmt.Sprintf("%s.options[%d]", at, j), f, maxOption)
			}
		}
		if d.Recommend == "" {
			c.need(d.Why != "", "%s: no recommendation; say why in why", at)
		} else {
			c.need(ids[d.Recommend], "%s.recommend: %q is not one of its options", at, d.Recommend)
		}
	}
}

// sources checks ids and that every [n] in the page names one.
func (c *checker) sources(b Brief) {
	ids := map[int]bool{}
	for i, s := range b.Sources {
		c.need(s.ID > 0 && !ids[s.ID], "sources[%d].id: %d, want a unique number from 1", i, s.ID)
		ids[s.ID] = true
		c.need(s.Title != "", "sources[%d]: no title", i)
		c.need(s.URL != "" || s.Quote != "", "sources[%d]: give a url or a quote so it can be checked", i)
		if s.URL != "" {
			u, err := url.Parse(s.URL)
			c.need(err == nil && (u.Scheme == "https" || u.Scheme == "http"), "sources[%d].url: %q, want an http(s) link", i, s.URL)
		}
	}
	for _, t := range texts(b) {
		for _, m := range citeRe.FindAllStringSubmatch(codeRe.ReplaceAllString(t, ""), -1) {
			n, _ := strconv.Atoi(m[1]) // the regexp only matches digits
			c.need(ids[n], "cite [%d]: no source with that id", n)
		}
	}
}

// texts is every text field that may cite.
func texts(b Brief) []string {
	out := append([]string{b.Summary.Answer, b.Summary.Ask}, b.Summary.Points...)
	if b.Confidence != nil {
		out = append(out, b.Confidence.Basis)
	}
	for _, s := range b.Thread {
		out = append(out, s.Text)
	}
	for _, s := range b.Sections {
		out = append(out, s.Text)
		if s.Table != nil {
			for _, r := range s.Table.Rows {
				out = append(out, r...)
			}
		}
	}
	for _, r := range b.Status {
		out = append(out, r.Note, r.Next)
	}
	for _, d := range b.Decisions {
		out = append(out, d.Context, d.Why)
		for _, o := range d.Options {
			out = append(out, o.Consequence, o.Cost, o.Risk)
		}
	}
	for _, d := range b.Details {
		out = append(out, d.Text)
	}
	return append(out, b.Defaults...)
}
