package routes

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/conf"
)

var day = time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)

func TestLoadMissingIsEmpty(t *testing.T) {
	rs, err := Load(t.TempDir())
	if err != nil || len(rs) != 0 {
		t.Fatalf("got %v %v", rs, err)
	}
}

func TestLoadHandWrittenKeepsUnknownKeys(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(Path(home), []byte(`# mine
[route.inbox]   # comms
triggers = ["check my mail", "draft replies"]
dir = "~/work/biz"
worktree = false
rules = ["draft only"]
effort = "high"
[other]
x = "y"
`), 0o600)
	rs, err := Load(home)
	if err != nil || len(rs) != 1 {
		t.Fatalf("%v %v", rs, err)
	}
	r := rs[0]
	if r.Name != "inbox" || r.Dir != "~/work/biz" || r.Worktree != "false" || len(r.Triggers) != 2 {
		t.Fatalf("%+v", r)
	}
	if _, _, err := Set(home, "inbox", map[string]conf.Value{"effort": "light"}, "", day); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path(home))
	if !strings.Contains(string(b), `rules = ["draft only"]`) || !strings.Contains(string(b), "[other]\nx = \"y\"") {
		t.Fatalf("unknown key or section dropped:\n%s", b)
	}
	if strings.Contains(string(b), "high") {
		t.Fatalf("the bad effort survived the set:\n%s", b)
	}
}

func TestSetRoundTripAndChanges(t *testing.T) {
	home := t.TempDir()
	prompt := "Act as \"comms\".\n\n{task}\n\tC:\\path #not a comment"
	_, ch, err := Set(home, "inbox", map[string]conf.Value{
		"description": "Messages and mail: draft replies", "dir": "/a", "profile": "fake",
		"may": []string{"merge-local"}, "interactive": "true", "prompt": prompt,
	}, "", day)
	if err != nil || len(ch) != 6 {
		t.Fatalf("%v %v", ch, err)
	}
	r, ch, err := Set(home, "inbox", map[string]conf.Value{"dir": "/b", "profile": "fake"}, "user: that goes to b", day)
	if err != nil || len(ch) != 1 || ch[0] != (Change{"dir", "/a", "/b"}) {
		t.Fatalf("%v %v", ch, err)
	}
	if r.LearnedFrom != "user: that goes to b" || r.Updated != "2026-11-13" {
		t.Fatalf("%+v", r)
	}
	got, err := Get(home, "inbox")
	if err != nil || got.Prompt != prompt || got.Dir != "/b" || got.Interactive != "true" || got.May[0] != "merge-local" {
		t.Fatalf("%+v %v", got, err)
	}
	if fi, _ := os.Stat(Path(home)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if _, _, err := Set(home, "inbox", map[string]conf.Value{"dir": ""}, "", day); err != nil {
		t.Fatal(err)
	}
	if got, _ := Get(home, "inbox"); got.Dir != "" {
		t.Fatalf("dir not unset: %q", got.Dir)
	}
}

func TestSetRejects(t *testing.T) {
	home := t.TempDir()
	for _, c := range []struct {
		name string
		f    map[string]conf.Value
	}{
		{"Bad.Name", nil},
		{"a", map[string]conf.Value{"effort": "max"}},
		{"a", map[string]conf.Value{"worktree": "maybe"}},
		{"a", map[string]conf.Value{"colour": "x"}},
		{"a", map[string]conf.Value{"updated": "2020-01-01"}},
	} {
		if _, _, err := Set(home, c.name, c.f, "", day); err == nil {
			t.Errorf("%s %v: no error", c.name, c.f)
		}
	}
	if _, err := os.Stat(Path(home)); err == nil {
		t.Fatal("a rejected set wrote the file")
	}
}

func TestRemoveAndIndex(t *testing.T) {
	home := t.TempDir()
	Set(home, "b", map[string]conf.Value{"triggers": []string{"fix a fleet bug", "in fleet"}}, "", day)
	Set(home, "a", map[string]conf.Value{"description": strings.Repeat("x", 100)}, "", day)
	rs, _ := Load(home)
	idx := Index(rs)
	if !strings.HasPrefix(idx, "- a: xxx") || !strings.Contains(idx, "…\n- b: fix a fleet bug, in fleet\n") {
		t.Fatalf("%q", idx)
	}
	if err := Remove(home, "a"); err != nil {
		t.Fatal(err)
	}
	if err := Remove(home, "a"); err == nil || !strings.Contains(err.Error(), "routes: b") {
		t.Fatalf("%v", err)
	}
}

func TestPromptFor(t *testing.T) {
	for _, c := range []struct {
		r    Route
		task string
		want string
	}{
		{Route{}, "do x", "do x"},
		{Route{Prompt: "Pre."}, "do x", "Pre.\n\ndo x"},
		{Route{Prompt: "Pre. {task} Post."}, "do x", "Pre. do x Post."},
		{Route{Prompt: "Pre."}, "", "Pre."},
		{Route{Skill: "inbox", Prompt: "Pre."}, "do x", "Use the inbox skill for this task.\n\nPre.\n\ndo x"},
	} {
		if got := c.r.PromptFor(c.task); got != c.want {
			t.Errorf("%+v %q: %q", c.r, c.task, got)
		}
	}
}

// The shipped example loads with every key known to fleet.
func TestExampleLoads(t *testing.T) {
	home := t.TempDir()
	b, err := os.ReadFile("../../examples/routes.toml.example")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(Path(home), b, 0o600)
	rs, err := Load(home)
	if err != nil || len(rs) != 3 {
		t.Fatalf("%v %v", rs, err)
	}
	for _, r := range rs {
		if len(r.Extra) > 0 {
			t.Errorf("%s: unknown keys %v", r.Name, r.Extra)
		}
	}
	if !strings.Contains(rs[2].Prompt, "\n\n{task}") {
		t.Errorf("prompt %q", rs[2].Prompt)
	}
}

// Values ending in a backslash or holding quotes, commas and # round-trip.
func TestSetEscapes(t *testing.T) {
	home := t.TempDir()
	tr := []string{`C:\`, `say "hi", # not a comment`, `a\\`}
	if _, _, err := Set(home, "a", map[string]conf.Value{"triggers": tr, "dir": `D:\`}, "", day); err != nil {
		t.Fatal(err)
	}
	r, err := Get(home, "a")
	if err != nil || r.Dir != `D:\` || strings.Join(r.Triggers, "|") != strings.Join(tr, "|") {
		t.Fatalf("%q %q %v", r.Dir, r.Triggers, err)
	}
}
