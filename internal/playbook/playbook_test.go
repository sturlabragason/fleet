package playbook

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture: <root>/repo is a git repo (a .git dir) with a src/sub subdir.
func fixture(t *testing.T) (home, root, repo string) {
	t.Helper()
	root = t.TempDir()
	home = filepath.Join(root, "home")
	repo = filepath.Join(root, "repo")
	os.MkdirAll(home, 0o755)
	os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	os.MkdirAll(filepath.Join(repo, "src", "sub"), 0o755)
	return
}

func TestDefaultIsEmbedded(t *testing.T) {
	if !strings.HasPrefix(Default(), "# Fleet worker playbook") {
		t.Fatalf("default: %.40q", Default())
	}
}

func TestResolveBuiltInOnly(t *testing.T) {
	home, _, repo := fixture(t)
	text, src := Resolve(Pro, home, repo)
	if text != Default() || !reflect.DeepEqual(src, []string{BuiltIn + " (pro)"}) {
		t.Fatalf("got %v", src)
	}
}

func TestResolveHomeAndRepoAppend(t *testing.T) {
	home, _, repo := fixture(t)
	hf := filepath.Join(home, "worker.md")
	rf := filepath.Join(repo, ".fleet", "worker.md")
	put(t, hf, "HOME RULE\n")
	text, src := Resolve(Pro, home, repo)
	if !strings.HasPrefix(text, Default()[:20]) || !strings.HasSuffix(text, "\n\nHOME RULE") {
		t.Fatalf("home append: %q", text[len(text)-30:])
	}
	if !reflect.DeepEqual(src, []string{BuiltIn + " (pro)", hf}) {
		t.Fatalf("sources %v", src)
	}
	put(t, rf, "REPO RULE")
	text, src = Resolve(Pro, home, repo)
	if !strings.HasSuffix(text, "HOME RULE\n\nREPO RULE") || !strings.Contains(text, "Fleet worker playbook") {
		t.Fatalf("repo append: %q", text)
	}
	if !reflect.DeepEqual(src, []string{BuiltIn + " (pro)", hf, rf}) {
		t.Fatalf("sources %v", src)
	}
}

func TestResolveEmptyHomeIgnored(t *testing.T) {
	home, _, repo := fixture(t)
	put(t, filepath.Join(home, "worker.md"), "  \n\n")
	if _, src := Resolve(Pro, home, repo); len(src) != 1 {
		t.Fatalf("sources %v", src)
	}
}

func TestResolveRepoReplace(t *testing.T) {
	home, _, repo := fixture(t)
	put(t, filepath.Join(home, "worker.md"), "HOME RULE")
	put(t, filepath.Join(repo, ".fleet", "worker.md"), ReplaceMarker+"\nONLY THIS\n")
	text, _ := Resolve(Pro, home, repo)
	if text != "ONLY THIS" {
		t.Fatalf("replace: %q", text)
	}
}

func TestResolveRepoOptOut(t *testing.T) {
	home, _, repo := fixture(t)
	put(t, filepath.Join(repo, ".fleet", "worker.md"), "  "+ReplaceMarker+"  \n\n")
	text, src := Resolve(Pro, home, repo)
	if text != "" || len(src) != 2 {
		t.Fatalf("opt-out: %q %v", text, src)
	}
	if got := Compose(text, "do it"); got != "do it" {
		t.Fatalf("compose after opt-out: %q", got)
	}
}

func TestResolveWalksUpFromSubdir(t *testing.T) {
	home, _, repo := fixture(t)
	rf := filepath.Join(repo, ".fleet", "worker.md")
	put(t, rf, "REPO RULE")
	text, src := Resolve(Pro, home, filepath.Join(repo, "src", "sub"))
	if !strings.HasSuffix(text, "REPO RULE") || src[len(src)-1] != rf {
		t.Fatalf("walk-up: %v", src)
	}
}

// ~/.fleet/worker.md is the user layer; walking up out of a non-repo dir
// must not pick it up a second time as a repo layer (ADR-58).
func TestResolveStopsAtTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	user := filepath.Join(home, ".fleet", "worker.md")
	put(t, user, "USER RULE")
	dir := filepath.Join(home, "scratch", "notes")
	os.MkdirAll(dir, 0o755)
	text, src := Resolve(Pro, filepath.Join(home, ".fleet"), dir)
	if n := strings.Count(text, "USER RULE"); n != 1 || len(src) != 2 || src[1] != user {
		t.Fatalf("user layer applied %d times, sources %v", n, src)
	}
}

// Ceiling pins where the walk stops, so a test suite never reads a layer
// from outside its temp dir whatever HOME says (ADR-62).
func TestResolveStopsAtTheCeiling(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	put(t, filepath.Join(root, ".fleet", "worker.md"), "OUTSIDE")
	put(t, filepath.Join(root, "top", ".fleet", "worker.md"), "AT THE CEILING")
	dir := filepath.Join(root, "top", "a", "b")
	os.MkdirAll(dir, 0o755)
	old := Ceiling
	Ceiling = filepath.Join(root, "top")
	t.Cleanup(func() { Ceiling = old })
	if text, src := Resolve(Pro, t.TempDir(), dir); strings.Contains(text, "OUTSIDE") || strings.Contains(text, "CEILING") || len(src) != 1 {
		t.Fatalf("walked past the ceiling: %v", src)
	}
}

func TestResolveStopsAtGitBoundary(t *testing.T) {
	home, root, repo := fixture(t)
	put(t, filepath.Join(root, ".fleet", "worker.md"), "ABOVE THE REPO")
	text, src := Resolve(Pro, home, filepath.Join(repo, "src"))
	if strings.Contains(text, "ABOVE") || len(src) != 1 {
		t.Fatalf("crossed .git dir: %v", src)
	}
	// A worktree has a .git file, which is a boundary too.
	wt := filepath.Join(root, "wt")
	put(t, filepath.Join(wt, ".git"), "gitdir: /elsewhere\n")
	os.MkdirAll(filepath.Join(wt, "pkg"), 0o755)
	text, src = Resolve(Pro, home, filepath.Join(wt, "pkg"))
	if strings.Contains(text, "ABOVE") || len(src) != 1 {
		t.Fatalf("crossed .git file: %v", src)
	}
}

func TestCompose(t *testing.T) {
	if got := Compose("PLAY", ""); got != "" {
		t.Errorf("empty prompt: %q", got)
	}
	if got := Compose("", "task"); got != "task" {
		t.Errorf("empty playbook: %q", got)
	}
	if got := Compose("PLAY\n\n", "fix the bug"); got != "PLAY\n\n## Your task\n\nfix the bug" {
		t.Errorf("compose: %q", got)
	}
}

// timeRule is the user's wording; both injected texts must carry it verbatim.
const timeRule = "Time matters here: do not spend time that can be avoided, and the earlier a correct result is obtained, the better."

func TestDefaultCarriesStandingRules(t *testing.T) {
	d := Default()
	if !strings.Contains(d, timeRule) {
		t.Error("worker playbook lost the time rule")
	}
	if i := strings.Index(d, timeRule); i < 0 || i > strings.Index(d, "\nStart\n") {
		t.Error("time rule must come before the Start section")
	}
	if !strings.Contains(d, "squash fixups into their logical commits") ||
		!strings.Contains(d, "published main is never rewritten") {
		t.Error("worker playbook lost the squash-before-merge rule")
	}
	// ADR-39: spawn already made the worktree, and step 7 must not delete the cwd.
	if !strings.Contains(d, "Fleet usually starts you in one") {
		t.Error("worker playbook must tell agents to stay in the worktree spawn made")
	}
	if strings.Contains(d, "remove your worktree") {
		t.Error("worker playbook must not tell agents to remove their own worktree")
	}
	if !strings.Contains(d, "Leave your own worktree and branch in place") {
		t.Error("worker playbook lost the leave-the-worktree rule")
	}
	// Process review (docs/process-review.md): merge order is read, ADR numbers are given, and the two shared
	// files conflict by construction.
	if !strings.Contains(d, "Merge order comes from branch state, not from a reply: fewer touched files goes first") {
		t.Error("worker playbook lost the merge-order rule")
	}
	if !strings.Contains(d, "Your ADR number comes from your task prompt") {
		t.Error("worker playbook lost the ADR-number rule")
	}
	if !strings.Contains(d, "CHANGELOG.md and DESIGN.md conflict at the top of today's section by construction") {
		t.Error("worker playbook lost the shared-file conflict rule")
	}
}

// The playbook is paid for on every spawn, so DESIGN §4 pins its size.
func TestDefaultStaysShort(t *testing.T) {
	if n := strings.Count(strings.TrimSpace(Default()), "\n") + 1; n > 40 {
		t.Errorf("playbook grew to %d lines; DESIGN §4 budgets about 40", n)
	}
}

// ADR-57: the level decides how much process a spawn pays for.
func TestParseLevel(t *testing.T) {
	for _, in := range []string{"light", "LIGHT", " standard ", "pro"} {
		l, err := ParseLevel(in)
		if err != nil || l != Level(strings.ToLower(strings.TrimSpace(in))) {
			t.Errorf("ParseLevel(%q) = %q, %v", in, l, err)
		}
	}
	for _, in := range []string{"", "full", "medium", "pro-max"} {
		if l, err := ParseLevel(in); err == nil {
			t.Errorf("ParseLevel(%q) = %q, want an error", in, l)
		}
	}
}

// A git worktree means professional code work; anything else is an errand
// until the spawn says otherwise.
func TestDefaultLevel(t *testing.T) {
	if got := DefaultLevel(true); got != Pro {
		t.Errorf("with a worktree: %q", got)
	}
	if got := DefaultLevel(false); got != Light {
		t.Errorf("without a worktree: %q", got)
	}
}

func TestTextPerLevel(t *testing.T) {
	seen := map[string]Level{}
	for _, l := range Levels {
		txt := Text(l)
		if !strings.HasPrefix(txt, "# Fleet worker playbook") {
			t.Fatalf("%s: %.40q", l, txt)
		}
		if other, dup := seen[txt]; dup {
			t.Fatalf("%s and %s are the same text", l, other)
		}
		seen[txt] = l
	}
	if Text(Pro) != Default() {
		t.Error("pro must be the full built-in playbook")
	}
	if Text(Level("nonsense")) != Default() {
		t.Error("an unknown level must fall back to pro")
	}
}

// Each level gets the rules it is for, and not the ceremony of the one above.
func TestLevelsCarryTheirOwnRules(t *testing.T) {
	light, std, pro := Text(Light), Text(Standard), Text(Pro)
	for _, l := range []string{light, std, pro} {
		if !strings.Contains(l, "fleet_report") || !strings.Contains(l, "Time matters here") {
			t.Error("every level must keep the report rule and the time rule")
		}
	}
	for _, want := range []string{"secrets", "fleet report done"} {
		if !strings.Contains(light, want) {
			t.Errorf("light lost %q", want)
		}
	}
	for _, unwanted := range []string{"Pre-merge checklist", "subagent", "ADR", "fleet touching"} {
		if strings.Contains(light, unwanted) {
			t.Errorf("light must not carry %q", unwanted)
		}
		if strings.Contains(std, unwanted) {
			t.Errorf("standard must not carry %q", unwanted)
		}
	}
	for _, want := range []string{"Assisted-by:", "Never push main", "Plan in two or three lines"} {
		if !strings.Contains(std, want) {
			t.Errorf("standard lost %q", want)
		}
	}
}

// safetyCore is the rule set no effort level may drop (ADR-59): light is the
// default for a worktree=false spawn, which lands in the user's own checkout.
var safetyCore = map[string]string{
	"read-only":       "say so in your first reply and edit nothing",
	"never push main": "never push main",
	"no force-push":   "never force-push or rewrite pushed history",
	"assisted-by":     "an `assisted-by: <tool>` trailer",
	"no ai co-author": "never a `co-authored-by:` for an ai",
	"no secrets":      "secrets and private data",
	"own branch":      "if you are not on a `fleet/` branch, create one",
}

func TestEveryLevelCarriesTheSafetyCore(t *testing.T) {
	for _, l := range Levels {
		txt := strings.ToLower(Text(l))
		for rule, want := range safetyCore {
			if !strings.Contains(txt, want) {
				t.Errorf("%s playbook lost the %s rule (%q)", l, rule, want)
			}
		}
	}
}

// standard used to say both "stay in the directory" and "never work in the
// user's main checkout", with no way out for an agent started in one.
func TestStandardDoesNotContradictItself(t *testing.T) {
	if strings.Contains(Text(Standard), "never work in the user's main checkout") {
		t.Error("standard still forbids the dir it tells the agent to stay in")
	}
}

// The playbook is paid for on every spawn; the lighter levels exist to be
// small, so their size is pinned too (DESIGN §4, ADR-57).
func TestLightAndStandardStayShort(t *testing.T) {
	for _, c := range []struct {
		level Level
		max   int
	}{{Light, 12}, {Standard, 24}} {
		if n := strings.Count(strings.TrimSpace(Text(c.level)), "\n") + 1; n > c.max {
			t.Errorf("%s playbook grew to %d lines; DESIGN §4 budgets about %d", c.level, n, c.max)
		}
	}
}

func TestResolveLayersOntoEveryLevel(t *testing.T) {
	home, _, repo := fixture(t)
	put(t, filepath.Join(home, "worker.md"), "HOME RULE")
	for _, l := range Levels {
		text, src := Resolve(l, home, repo)
		if !strings.HasSuffix(text, "HOME RULE") || !strings.HasPrefix(text, Text(l)[:20]) {
			t.Errorf("%s: %q", l, text)
		}
		if src[0] != BuiltIn+" ("+string(l)+")" {
			t.Errorf("%s: sources %v", l, src)
		}
	}
}

// Heavy checks run in CI on the agent's pushed branch (ADR-69): pro and
// standard push only their own fleet/ branch and test locally by package.
func TestProAndStandardPushOnlyTheirBranch(t *testing.T) {
	for _, l := range []Level{Pro, Standard} {
		txt := Text(l)
		for _, want := range []string{"git push -u origin fleet/<name>", "never push main", "go test ./<pkg>/...", "gh run watch"} {
			if !strings.Contains(strings.ToLower(txt), strings.ToLower(want)) {
				t.Errorf("%s playbook lost %q", l, want)
			}
		}
	}
	if !strings.Contains(Text(Light), "Never push main or any branch") {
		t.Error("light must push nothing")
	}
}
