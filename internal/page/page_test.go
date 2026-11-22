package page

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func valid() Brief {
	return Brief{
		Page: "decide-x", Kind: "decision", Title: "Use B", Date: "2026-11-22",
		Summary:    Summary{Answer: "Use B: it is cheaper [1].", Points: []string{"one"}, Ask: "Pick one."},
		Confidence: &Confidence{Level: "medium", Basis: "two sources"},
		Thread:     []Step{{Label: "Evidence", Text: "a"}, {Label: "Answer", Text: "b"}},
		Decisions: []Decision{
			{ID: "arch", Question: "Which?", Options: []Option{{ID: "A", Label: "A"}, {ID: "B", Label: "B <b>"}}, Recommend: "B"},
			{ID: "open", Question: "Open?", Options: []Option{{ID: "y", Label: "yes"}, {ID: "n", Label: "no"}}, Why: "your call"},
		},
		Defaults: []string{"keep names"},
		Sources:  []Source{{ID: 1, Title: "Src", URL: "https://example.com", Quote: "cheaper \"wins\""}},
	}
}

func TestCheck(t *testing.T) {
	long := strings.Repeat("word ", 50)
	cases := []struct {
		name string
		edit func(*Brief)
		want string // substring of a problem; "" = none
	}{
		{"valid", func(*Brief) {}, ""},
		{"wall of text", func(b *Brief) { b.Summary.Answer = long }, "summary.answer: 50 words"},
		{"no confidence", func(b *Brief) { b.Confidence = nil }, "confidence:"},
		{"no sources", func(b *Brief) { b.Sources = nil }, "sources: a decision page"},
		{"dangling cite", func(b *Brief) { b.Summary.Points = []string{"see [7]"} }, "cite [7]"},
		{"bad recommend", func(b *Brief) { b.Decisions[0].Recommend = "Z" }, "recommend"},
		{"no rec, no why", func(b *Brief) { b.Decisions[1].Why = "" }, "no recommendation"},
		{"one option", func(b *Brief) { b.Decisions[0].Options = b.Decisions[0].Options[:1] }, "1 options"},
		{"reserved later", func(b *Brief) { b.Decisions[0].Options[0].ID = Later }, "not \"later\""},
		{"reserved key", func(b *Brief) { b.Decisions[0].ID = "note" }, "decisions[0].id"},
		{"wrong prefix", func(b *Brief) { b.Page = "my-page" }, "decide-"},
		{"too many cards", func(b *Brief) {
			for len(b.Decisions) < 16 {
				d := b.Decisions[0]
				d.ID = "d" + string(rune('a'+len(b.Decisions)))
				b.Decisions = append(b.Decisions, d)
			}
		}, "at most 15"},
		{"code is not a cite", func(b *Brief) { b.Summary.Points = []string{"use `x[0]`"} }, ""},
		{"cite in a table", func(b *Brief) {
			b.Sections = []Section{{Heading: "h", Table: &Table{Columns: []string{"a"}, Rows: [][]string{{"see [7]"}}}}}
		}, "cite [7]"},
		{"script url", func(b *Brief) { b.Sources[0].URL = "javascript:alert(1)" }, "http(s) link"},
		{"huge round", func(b *Brief) { b.Round = 100000 }, "round:"},
		{"bad kind", func(b *Brief) { b.Kind = "memo" }, "kind:"},
		{"table shape", func(b *Brief) {
			b.Sections = []Section{{Heading: "h", Table: &Table{Columns: []string{"a", "b"}, Rows: [][]string{{"1"}}}}}
		}, "1 cells for 2 columns"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := valid()
			c.edit(&b)
			got := strings.Join(Check(b), "\n")
			if c.want == "" && got != "" {
				t.Fatalf("want no problems, got:\n%s", got)
			}
			if !strings.Contains(got, c.want) {
				t.Fatalf("want %q among:\n%s", c.want, got)
			}
		})
	}
}

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	if err := Write(valid(), dir); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(page)
	for _, want := range []string{
		`data-decision="decide-x-1"`,
		`B &lt;b&gt;`, // escaped
		`class="cite" href="#src-1" data-q="Src: “cheaper &#34;wins&#34;”">[1]`, // cite carries its quote
		`value="later" checked`, // no recommendation: decide later
		`value="B" checked`,     // recommendation preselected
		`<span class="badge ok">recommended</span>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if n := strings.Count(s, SendToken); n != 1 {
		t.Errorf("send token %d times, the host wants exactly 1", n)
	}
	if n := strings.Count(s, "<textarea rows=\"1\""); n != 2 {
		t.Errorf("%d free-text boxes, want one per card (2)", n)
	}
	var sc Schema
	data, err := os.ReadFile(filepath.Join(dir, "answer.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &sc); err != nil {
		t.Fatal(err)
	}
	if len(sc.Items) != 3 || sc.Items[1].Choices[2] != Later || sc.Items[0].Text != freeText || sc.Items[2].Defaults != 1 {
		t.Errorf("schema = %+v", sc)
	}
}

func TestWriteNoAsks(t *testing.T) {
	b := valid()
	b.Page, b.Kind, b.Decisions, b.Defaults = "status-x", "status", nil, nil
	b.Status = []StatusRow{{Item: "ci", State: "ok"}}
	if p := Check(b); len(p) != 0 {
		t.Fatalf("problems: %v", p)
	}
	dir := t.TempDir()
	if err := Write(b, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "answer.json")); !os.IsNotExist(err) {
		t.Errorf("a page without asks has an answer.json: %v", err)
	}
	page, _ := os.ReadFile(filepath.Join(dir, "index.html"))
	if strings.Contains(string(page), SendToken) {
		t.Error("a page without asks carries a send token")
	}
}

// The example the docs point at must stay a valid brief.
func TestExample(t *testing.T) {
	b, err := Load("../../examples/page-brief.json")
	if err != nil {
		t.Fatal(err)
	}
	if p := Check(b); len(p) > 0 {
		t.Fatalf("examples/page-brief.json: %v", p)
	}
}

func TestRich(t *testing.T) {
	q := map[int]string{1: "S"}
	cases := []struct{ in, want string }{
		{"a [1] `x[1]`", `<p>a <a class="cite" href="#src-1" data-q="S">[1]</a> <code>x[1]</code></p>`},
		{"`a\n\nb`", "<p>`a</p><p>b`</p>"},
		{"<b>&", "<p>&lt;b&gt;&amp;</p>"},
	}
	for _, c := range cases {
		if got := string(rich(c.in, q, true)); got != c.want {
			t.Errorf("rich(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}
