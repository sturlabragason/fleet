package status

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func capture(t *testing.T, set, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", set, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Claude's folder-trust check and agy's are ❯ lists with no numbers on
// screen; they must still parse so fleet_answer can pick an option.
func TestPlainMenuTrust(t *testing.T) {
	sets := Load(t.TempDir())
	m, ok := sets["claude"].Menu(capture(t, "claude", "waiting-trust.txt"))
	if !ok {
		t.Fatal("claude trust menu not found")
	}
	if m.Numbered {
		t.Error("options are not numbered on screen")
	}
	if len(m.Options) != 2 || Label(m.Options[0]) != "No, exit" ||
		!strings.HasPrefix(Label(m.Options[1]), "Yes, I trust this folder") {
		t.Fatalf("options: %q", m.Options)
	}
	if m.Selected != 1 {
		t.Errorf("selected %d, want the cursor on 1", m.Selected)
	}
	if !strings.Contains(m.Question, "trust") {
		t.Errorf("question: %q", m.Question)
	}
	if m, ok := sets["agy"].Menu(capture(t, "agy", "waiting-trust.txt")); !ok || len(m.Options) < 2 {
		t.Fatalf("agy trust: %v %+v", ok, m)
	}
}

// Claude's external-import dialog: the question sits a paragraph, a path
// list and a security note above the options, and the only line a pattern
// matches is the key hint below them. The menu still has to carry the
// question, because that is what the user is asked and what fleet shows as
// waiting_for.
func TestPlainMenuImports(t *testing.T) {
	s := Load(t.TempDir())["claude"]
	txt := capture(t, "claude", "waiting-imports.txt")
	if st, _, ok := s.Match(txt); !ok || st != Waiting {
		t.Fatalf("status %v ok=%v, want waiting", st, ok)
	}
	m, ok := s.Menu(txt)
	if !ok || len(m.Options) != 2 || m.Selected != 1 {
		t.Fatalf("menu: %v %+v", ok, m)
	}
	if m.Question != "Allow external CLAUDE.md file imports?" {
		t.Errorf("question %q", m.Question)
	}
	if Label(m.Options[1]) != "Yes, allow external imports" {
		t.Errorf("options %q", m.Options)
	}
}

// Nothing that is not a prompt may parse as a menu: an idle input box ("❯ "
// with typed text) and a working screen are the dangerous near-misses.
func TestPlainMenuNoFalsePositives(t *testing.T) {
	sets := Load(t.TempDir())
	files, _ := filepath.Glob("testdata/*/*.txt")
	if len(files) == 0 {
		t.Fatal("no testdata")
	}
	for _, f := range files {
		set := filepath.Base(filepath.Dir(f))
		name := filepath.Base(f)
		if strings.HasPrefix(name, "waiting") {
			continue
		}
		b, _ := os.ReadFile(f)
		if m, ok := sets[set].Menu(string(b)); ok {
			t.Errorf("%s/%s: menu in a non-waiting screen: %+v", set, name, m)
		}
	}
	if _, ok := ParseMenu("❯ write me a haiku about tmux\n"); ok {
		t.Error("input box taken for a menu")
	}
}

// A ❯ run needs two option lines and exactly one cursor; box rules and
// over-long lines are not options.
func TestPlainMenuShape(t *testing.T) {
	if _, ok := ParseMenu("❯ yes\n"); ok {
		t.Error("single option taken for a menu")
	}
	if _, ok := ParseMenu("❯ yes\n❯ no\n"); ok {
		t.Error("two cursors taken for a menu")
	}
	if _, ok := ParseMenu("❯ ─────────\n  ─────────\n"); ok {
		t.Error("box rule taken for options")
	}
	long := strings.Repeat("x", plainMax+1)
	if _, ok := ParseMenu("❯ yes\n  " + long + "\n"); ok {
		t.Error("over-long line taken for an option")
	}
	m, ok := ParseMenu("Trust this folder?\n\n❯ No, exit\n  Yes, proceed\n")
	if !ok || m.Options[1] != "2. Yes, proceed" || m.Question != "Trust this folder?" {
		t.Fatalf("plain menu: %v %+v", ok, m)
	}
}

// ">" is agy's cursor but also starts a quoted shell command in an agent's
// own output. A quoted block with no key hint under it is not a menu.
func TestQuotedBlockIsNotAMenu(t *testing.T) {
	screen := strings.Join([]string{
		"I ran the checks you asked for:",
		"",
		"> go test ./...",
		"  ok   github.com/sturlabragason/fleet/internal/status",
		"",
		"All green.",
	}, "\n")
	if m, ok := (Set{}).Menu(screen); ok {
		t.Errorf("quoted command read as a menu: %+v", m)
	}
}
