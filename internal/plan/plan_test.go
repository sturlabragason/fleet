package plan

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const twoTasks = `
[task.schema]
profile = "claude"
dir = "app"
prompt = "add the users table"
check = "make test"

[task.api]
profile = "copilot"
dir = "~/app"
prompt = "serve /users"
after = ["schema"]
review = { profile = "claude", max_rounds = 3 }
`

func TestParseReadsTasksDepsAndReview(t *testing.T) {
	p, err := Parse("ship", twoTasks)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Tasks) != 2 || p.Tasks[0].Name != "api" || p.Tasks[1].Name != "schema" {
		t.Fatalf("tasks %+v", p.Tasks)
	}
	api := p.Task("api")
	if api.Review == nil || api.Review.Profile != "claude" || api.Review.MaxRounds != 3 || api.After[0] != "schema" {
		t.Fatalf("api %+v review %+v", api, api.Review)
	}
	if p.Task("schema").Check != "make test" || p.Task("schema").Review != nil {
		t.Fatalf("schema %+v", p.Task("schema"))
	}
	if err := p.ResolveDirs("/base"); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if p.Task("schema").Dir != "/base/app" || p.Task("api").Dir != filepath.Join(home, "app") {
		t.Fatalf("dirs %q %q", p.Task("schema").Dir, p.Task("api").Dir)
	}
	q, _ := Parse("ship", twoTasks)
	if err := q.ResolveDirs(""); err == nil {
		t.Fatal("relative dir accepted without a base")
	}
	r, _ := Parse("ship", "[task.a]\nprofile=\"x\"\ndir=\"/d\"\nprompt=\"p\"\nreview = { profile = \"y\" }\n")
	if r.Task("a").Review.MaxRounds != DefaultRounds {
		t.Fatalf("default rounds %d", r.Task("a").Review.MaxRounds)
	}
	if e, _ := Parse("ship", "[task.a]\nprofile=\"x\"\ndir=\"/d\"\nprompt=\"p\"\nafter = []\n"); e.Task("a").After != nil {
		t.Fatalf("after = [] kept as %#v; a resumed plan would not match", e.Task("a").After)
	}
}

func TestParseRefusesBadPlans(t *testing.T) {
	task := func(name, extra string) string {
		return "[task." + name + "]\nprofile = \"x\"\ndir = \"/d\"\nprompt = \"p\"\n" + extra + "\n"
	}
	for _, tc := range []struct{ name, text, want string }{
		{"empty", "", "no [task.<name>]"},
		{"section", "[other]\nk = \"v\"\n", "unknown section"},
		{"key", task("a", "colour = \"red\""), "unknown key"},
		{"missing", "[task.a]\nprofile = \"x\"\n", "needs profile, dir and prompt"},
		{"dep", task("a", "after = [\"nope\"]"), "not a task of the plan"},
		{"cycle", task("a", "after = [\"b\"]") + task("b", "after = [\"a\"]"), "dependency cycle"},
		{"after not a list", task("a", "after = \"b\"") + task("b", ""), "after is a list"},
		{"rounds", task("a", "review = { profile = \"y\", max_rounds = 0 }"), "not a positive number"},
		{"clash", task("a", "review = { profile = \"y\" }") + task("a-review", ""), "clashes"},
		{"taskname", task("A", ""), "task name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse("p", tc.text); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
		})
	}
	if _, err := Parse("Bad Name", twoTasks); err == nil {
		t.Fatal("bad plan name accepted")
	}
}

func TestStateReadyAndStoreRoundTrip(t *testing.T) {
	p, _ := Parse("ship", twoTasks)
	s := New(p, "", time.Now())
	if r := s.Ready(); len(r) != 1 || r[0] != "schema" {
		t.Fatalf("ready %v", r)
	}
	s.Tasks["schema"].Phase = Done
	if r := s.Ready(); len(r) != 1 || r[0] != "api" {
		t.Fatalf("ready after schema %v", r)
	}
	home := t.TempDir()
	if _, err := Load(home, "ship"); !errors.Is(err, ErrNoPlan) {
		t.Fatalf("load before start: %v", err)
	}
	if err := Update(home, "ship", func(cur *State) (*State, error) { return s, nil }); err != nil {
		t.Fatal(err)
	}
	got, err := Load(home, "ship")
	if err != nil || got.Tasks["schema"].Phase != Done || got.Plan.Task("api").Review.MaxRounds != 3 {
		t.Fatalf("round trip %v %+v", err, got)
	}
	if l := List(home); len(l) != 1 || l[0] != "ship" {
		t.Fatalf("list %v", l)
	}
	if got.Finished() {
		t.Fatal("finished with api pending")
	}
}
