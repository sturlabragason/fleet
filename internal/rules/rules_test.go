package rules

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/playbook"
)

func TestMain(m *testing.M) {
	playbook.Ceiling = os.TempDir() // never read the user's real layers (ADR-62)
	os.Exit(m.Run())
}

// A rule stated once reaches every later spawn: added through the CLI path to
// the user layer and to a repo layer, it is in playbook.Resolve at every
// effort level (ADR-131).
func TestRuleAddReachesPlaybook(t *testing.T) {
	root := t.TempDir()
	home, repo := filepath.Join(root, "home"), filepath.Join(root, "repo")
	os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	os.MkdirAll(filepath.Join(repo, "src"), 0o755)
	if _, added, err := Add(UserFile(home), "never send into a session\nI am typing in"); err != nil || !added {
		t.Fatalf("user add: %v %v", added, err)
	}
	rf, err := RepoFile(filepath.Join(repo, "src"))
	if err != nil || rf != filepath.Join(repo, ".fleet", "worker.md") {
		t.Fatalf("repo file %q %v", rf, err)
	}
	if _, _, err := Add(rf, "merge without asking in this repo"); err != nil {
		t.Fatal(err)
	}
	for _, l := range playbook.Levels {
		text, _ := playbook.Resolve(l, home, filepath.Join(repo, "src"))
		for _, want := range []string{Heading, "- never send into a session I am typing in", "- merge without asking in this repo"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: playbook lacks %q", l, want)
			}
		}
	}
}

func TestAddKeepsTheRestOfTheFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "worker.md")
	orig := "# Mine\n\nkeep  this\ttext\n\n## Standing user rules\n\n- one\n\n## Later\n\ntail\n"
	os.WriteFile(p, []byte(orig), 0o644)
	if _, added, err := Add(p, "two"); err != nil || !added {
		t.Fatal(added, err)
	}
	b, _ := os.ReadFile(p)
	if want := strings.Replace(orig, "- one\n", "- one\n- two\n", 1); string(b) != want {
		t.Fatalf("got %q\nwant %q", b, want)
	}
	if _, added, _ := Add(p, "  TWO "); added {
		t.Error("a duplicate rule was added")
	}
	got, _ := List(p)
	if !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Errorf("list %q", got)
	}
	if r, err := Remove(p, 1); err != nil || r != "one" {
		t.Fatal(r, err)
	}
	b, _ = os.ReadFile(p)
	if want := strings.Replace(orig, "- one\n", "- two\n", 1); string(b) != want {
		t.Fatalf("after rm %q", b)
	}
	if _, err := Remove(p, 2); err == nil {
		t.Error("rm past the end must fail")
	}
}

func TestAddCreatesTheFileAndHeading(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "worker.md")
	if _, _, err := Add(p, "a"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != Heading+"\n\n- a\n" {
		t.Fatalf("%q", b)
	}
	os.WriteFile(p, []byte("user text"), 0o644)
	Add(p, "b")
	b, _ = os.ReadFile(p)
	if string(b) != "user text\n\n"+Heading+"\n\n- b\n" {
		t.Fatalf("%q", b)
	}
}

func TestAddRefusesEmptyAndLong(t *testing.T) {
	p := filepath.Join(t.TempDir(), "worker.md")
	if _, _, err := Add(p, " \n "); err == nil {
		t.Error("empty rule accepted")
	}
	if _, _, err := Add(p, strings.Repeat("x", MaxLen+1)); err == nil || !strings.Contains(err.Error(), "every spawn") {
		t.Errorf("long rule: %v", err)
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("a refused rule wrote the file")
	}
}
