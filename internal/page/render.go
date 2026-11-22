package page

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	//go:embed assets/page.css
	pageCSS string
	//go:embed assets/page.tmpl
	pageTmpl string
	//go:embed assets/send.js
	sendJS string
)

// SendToken is the placeholder the artifact host swaps for a per-version
// token when it serves a page that has an answer.json (ADR-163).
const SendToken = "__ARTIFACT_SEND_TOKEN__"

var codeRe = regexp.MustCompile("`([^`\n]+)`")

// Render returns the page's HTML. It does not Check b.
func Render(b Brief) ([]byte, error) {
	t, err := template.New("page").Funcs(funcs(b)).Parse(pageTmpl)
	if err != nil {
		return nil, fmt.Errorf("parse page template: %w", err)
	}
	var buf bytes.Buffer
	err = t.Execute(&buf, map[string]any{
		"B": b, "CSS": template.CSS(pageCSS), "JS": template.JS(sendJS), // #nosec G203 -- our own embedded files
		"Token": SendToken, "Decision": b.DecisionID(), "Groups": groups(b.Decisions),
		"Minutes": minutes(b),
	})
	if err != nil {
		return nil, fmt.Errorf("render page: %w", err)
	}
	return buf.Bytes(), nil
}

// Write renders b into dir as index.html, plus answer.json when the page
// takes answers.
func Write(b Brief, dir string) error {
	out, err := Render(b)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("make page dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), out, 0o644); err != nil {
		return fmt.Errorf("write page: %w", err)
	}
	if !asks(b) {
		return nil
	}
	data, err := json.MarshalIndent(AnswerSchema(b), "", " ")
	if err != nil {
		return fmt.Errorf("encode answer schema: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "answer.json"), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write answer schema: %w", err)
	}
	return nil
}

func asks(b Brief) bool { return len(b.Decisions) > 0 || len(b.Defaults) > 0 }

// Item is one answer field the artifact host accepts.
type Item struct {
	Key      string   `json:"key"`
	Choices  []string `json:"choices,omitempty"`
	Text     int      `json:"text,omitempty"`
	Defaults int      `json:"defaults,omitempty"`
}

// Schema is answer.json: what Send may post, the shape the decision-page
// skill's check-page writes.
type Schema struct {
	Decision string `json:"decision"`
	Items    []Item `json:"items"`
	Note     int    `json:"note"`
}

// freeText is the cap on a card's free-text answer and on the note.
const freeText = 500

// AnswerSchema lists each card's options and its free-text box.
func AnswerSchema(b Brief) Schema {
	s := Schema{Decision: b.DecisionID(), Note: freeText}
	for _, d := range b.Decisions {
		it := Item{Key: d.ID, Text: freeText}
		for _, o := range d.Options {
			it.Choices = append(it.Choices, o.ID)
		}
		if d.Recommend == "" {
			it.Choices = append(it.Choices, Later)
		}
		s.Items = append(s.Items, it)
	}
	if len(b.Defaults) > 0 {
		s.Items = append(s.Items, Item{Key: "defaults", Defaults: len(b.Defaults)})
	}
	return s
}

// Group is the decisions under one heading, in brief order.
type Group struct {
	Name      string
	Decisions []Decision
}

func groups(ds []Decision) []Group {
	var out []Group
	at := map[string]int{}
	for _, d := range ds {
		i, ok := at[d.Group]
		if !ok {
			i = len(out)
			at[d.Group] = i
			out = append(out, Group{Name: d.Group})
		}
		out[i].Decisions = append(out[i].Decisions, d)
	}
	return out
}

// minutes estimates reading the unfolded page at 200 words a minute plus
// 20 seconds a decision.
func minutes(b Brief) int {
	words := 0
	for _, t := range texts(b) {
		words += len(strings.Fields(t))
	}
	for _, d := range b.Details {
		words -= len(strings.Fields(d.Text)) // folded: not read by default
	}
	return int(math.Ceil(float64(words)/200 + float64(len(b.Decisions))/3))
}

func funcs(b Brief) template.FuncMap {
	quotes := map[int]string{}
	for _, s := range b.Sources {
		quotes[s.ID] = s.Title
		if s.Quote != "" {
			quotes[s.ID] = s.Title + ": “" + s.Quote + "”"
		}
	}
	return template.FuncMap{
		"rich":   func(s string) template.HTML { return rich(s, quotes, true) },
		"inline": func(s string) template.HTML { return rich(s, quotes, false) },
		"pct":    func(v float64, bs *Bars) string { return strconv.FormatFloat(pct(v, bs), 'f', 1, 64) },
		"num":    func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) },
		"plus":   func(a, b int) int { return a + b },
	}
}

func pct(v float64, bs *Bars) float64 {
	top := 0.0
	for _, it := range bs.Items {
		top = math.Max(top, it.Value)
	}
	if top == 0 {
		return 0
	}
	return 100 * v / top
}

// rich escapes s, then turns `code` and, outside code, [n] cites into
// markup; paras wraps blank-line-separated paragraphs in <p>.
func rich(s string, quotes map[int]string, paras bool) template.HTML {
	var esc strings.Builder
	last := 0
	cite := func(t string) string {
		return citeRe.ReplaceAllStringFunc(html.EscapeString(t), func(m string) string {
			n, _ := strconv.Atoi(m[1 : len(m)-1]) // citeRe matched digits
			return fmt.Sprintf(`<a class="cite" href="#src-%d" data-q="%s">[%d]</a>`, n, html.EscapeString(quotes[n]), n)
		})
	}
	for _, m := range codeRe.FindAllStringSubmatchIndex(s, -1) {
		esc.WriteString(cite(s[last:m[0]]))
		esc.WriteString("<code>" + html.EscapeString(s[m[2]:m[3]]) + "</code>")
		last = m[1]
	}
	esc.WriteString(cite(s[last:]))
	if !paras {
		return template.HTML(esc.String()) // #nosec G203 -- escaped above; only our own tags added
	}
	var out strings.Builder
	for p := range strings.SplitSeq(esc.String(), "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			out.WriteString("<p>" + strings.ReplaceAll(p, "\n", "<br>") + "</p>")
		}
	}
	return template.HTML(out.String()) // #nosec G203 -- escaped above
}
