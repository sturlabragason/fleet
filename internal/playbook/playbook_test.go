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
	text, src := Resolve(home, repo)
	if text != Default() || !reflect.DeepEqual(src, []string{BuiltIn}) {
		t.Fatalf("got %v", src)
	}
}

func TestResolveHomeAndRepoAppend(t *testing.T) {
	home, _, repo := fixture(t)
	hf := filepath.Join(home, "worker.md")
	rf := filepath.Join(repo, ".fleet", "worker.md")
	put(t, hf, "HOME RULE\n")
	text, src := Resolve(home, repo)
	if !strings.HasPrefix(text, Default()[:20]) || !strings.HasSuffix(text, "\n\nHOME RULE") {
		t.Fatalf("home append: %q", text[len(text)-30:])
	}
	if !reflect.DeepEqual(src, []string{BuiltIn, hf}) {
		t.Fatalf("sources %v", src)
	}
	put(t, rf, "REPO RULE")
	text, src = Resolve(home, repo)
	if !strings.HasSuffix(text, "HOME RULE\n\nREPO RULE") || !strings.Contains(text, "Fleet worker playbook") {
		t.Fatalf("repo append: %q", text)
	}
	if !reflect.DeepEqual(src, []string{BuiltIn, hf, rf}) {
		t.Fatalf("sources %v", src)
	}
}

func TestResolveEmptyHomeIgnored(t *testing.T) {
	home, _, repo := fixture(t)
	put(t, filepath.Join(home, "worker.md"), "  \n\n")
	if _, src := Resolve(home, repo); len(src) != 1 {
		t.Fatalf("sources %v", src)
	}
}

func TestResolveRepoReplace(t *testing.T) {
	home, _, repo := fixture(t)
	put(t, filepath.Join(home, "worker.md"), "HOME RULE")
	put(t, filepath.Join(repo, ".fleet", "worker.md"), ReplaceMarker+"\nONLY THIS\n")
	text, _ := Resolve(home, repo)
	if text != "ONLY THIS" {
		t.Fatalf("replace: %q", text)
	}
}

func TestResolveRepoOptOut(t *testing.T) {
	home, _, repo := fixture(t)
	put(t, filepath.Join(repo, ".fleet", "worker.md"), "  "+ReplaceMarker+"  \n\n")
	text, src := Resolve(home, repo)
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
	text, src := Resolve(home, filepath.Join(repo, "src", "sub"))
	if !strings.HasSuffix(text, "REPO RULE") || src[len(src)-1] != rf {
		t.Fatalf("walk-up: %v", src)
	}
}

func TestResolveStopsAtGitBoundary(t *testing.T) {
	home, root, repo := fixture(t)
	put(t, filepath.Join(root, ".fleet", "worker.md"), "ABOVE THE REPO")
	text, src := Resolve(home, filepath.Join(repo, "src"))
	if strings.Contains(text, "ABOVE") || len(src) != 1 {
		t.Fatalf("crossed .git dir: %v", src)
	}
	// A worktree has a .git file, which is a boundary too.
	wt := filepath.Join(root, "wt")
	put(t, filepath.Join(wt, ".git"), "gitdir: /elsewhere\n")
	os.MkdirAll(filepath.Join(wt, "pkg"), 0o755)
	text, src = Resolve(home, filepath.Join(wt, "pkg"))
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
}
