package whatsnew

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	aifleet "github.com/sturlabragason/fleet"
)

func TestParse(t *testing.T) {
	md := `# What's new
- before any heading, ignored

## 2026-11-05
- Pick the admiral (66a2ece, ADR-27)
- Plain line
- Keeps (a bracket that is not refs)
- Only an ADR (ADR-28)
  not a bullet
## Notes
- outside a dated section
## 2026-13-40
- bad date, ignored
## 2026-11-04
- Older (abcdef1234)
`
	es := Parse(md)
	if len(es) != 5 {
		t.Fatalf("%d entries: %+v", len(es), es)
	}
	if e := es[0]; e.Date != "2026-11-05" || e.Text != "Pick the admiral" || strings.Join(e.Refs, ",") != "66a2ece,ADR-27" || strings.Join(e.Hashes, ",") != "66a2ece" || e.ID() != "66a2ece" {
		t.Errorf("refs: %+v id %s", e, e.ID())
	}
	if e := es[2]; e.Text != "Keeps (a bracket that is not refs)" || e.Refs != nil {
		t.Errorf("non-ref bracket: %+v", e)
	}
	if e := es[3]; e.Text != "Only an ADR" || len(e.Hashes) != 0 || e.ID() == "" || e.ID() == es[1].ID() {
		t.Errorf("ADR only: %+v id %q", e, e.ID())
	}
	if e := es[4]; e.Date != "2026-11-04" || e.ID() != "abcdef1234" {
		t.Errorf("second day: %+v", e)
	}
	// The id without a hash changes with the text.
	a := Entry{Date: "2026-11-05", Text: "x"}
	b := Entry{Date: "2026-11-05", Text: "y"}
	if a.ID() == b.ID() || a.ID() != (Entry{Date: "2026-11-05", Text: "x"}).ID() {
		t.Error("text id not stable or not text-dependent")
	}
}

func TestEmbeddedChangelog(t *testing.T) {
	es := Parse(aifleet.Changelog)
	if len(es) < 15 {
		t.Fatalf("only %d entries", len(es))
	}
	prev := ""
	for _, l := range strings.Split(aifleet.Changelog, "\n") {
		if !strings.HasPrefix(l, "## ") {
			continue
		}
		d := strings.TrimSpace(l[3:])
		if _, err := time.Parse(DateLayout, d); err != nil {
			t.Errorf("heading %q: %v", l, err)
		}
		if prev != "" && d >= prev {
			t.Errorf("day %s after %s: days must be strictly descending", d, prev)
		}
		prev = d
	}
	for _, e := range es {
		if strings.TrimSpace(e.Text) == "" {
			t.Errorf("empty entry on %s", e.Date)
		}
	}
}

func TestSeenRoundTrip(t *testing.T) {
	t.Setenv("FLEET_HOME", t.TempDir())
	t.Setenv("FLEET_CONFIG", filepath.Join(t.TempDir(), "none.toml"))
	seen, err := LoadSeen()
	if err != nil || len(seen) != 0 {
		t.Fatalf("fresh: %v %v", seen, err)
	}
	a, b := Entry{Date: "2026-11-05", Text: "a"}, Entry{Date: "2026-11-05", Text: "b", Hashes: []string{"1234567"}}
	if err := MarkSeen([]Entry{a}); err != nil {
		t.Fatal(err)
	}
	if err := MarkSeen([]Entry{b, a}); err != nil {
		t.Fatal(err)
	}
	seen, _ = LoadSeen()
	if len(seen) != 2 || !seen[a.ID()] || !seen["1234567"] {
		t.Errorf("seen %v", seen)
	}
	raw, _ := os.ReadFile(SeenPath())
	if strings.Count(string(raw), "\n") != 2 {
		t.Errorf("file %q", raw)
	}
}

func gitRepo(t *testing.T) (string, func(args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	// Commits() itself runs git with os.Environ(), so pin it there too.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	env := append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	run := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = env
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	return dir, run
}

func TestCommits(t *testing.T) {
	dir, run := gitRepo(t)
	commit := func(file, msg string) string {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(msg), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", file)
		run("commit", "-q", "-m", msg)
		return run("rev-parse", "--short", "HEAD")
	}
	commit("a", "init")
	kept := commit("b", "feat(ui): kept feature")
	cited := commit("c", "feat: cited feature")
	commit("CHANGELOG.md", "feat!: touches the changelog")
	commit("d", "fix: a fix")
	commit("e", "Add something non-conventional")
	run("checkout", "-q", "-b", "side")
	commit("f", "feat: on a side branch")
	run("checkout", "-q", "main")
	run("merge", "-q", "--no-ff", "-m", "feat: merge commit", "side")
	sideMerged := run("log", "-1", "--format=%h", "HEAD^2")

	today := time.Now().Format(DateLayout)
	curated := []Entry{{Date: today, Text: "curated", Hashes: []string{cited}}}
	es, err := Commits(dir, curated)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range es {
		got = append(got, e.Text)
		if !e.Commit || e.Date != today || len(e.Refs) != 1 {
			t.Errorf("entry %+v", e)
		}
	}
	want := "feat: on a side branch|feat(ui): kept feature"
	if strings.Join(got, "|") != want {
		t.Errorf("got %q want %q (kept %s, side %s)", got, want, kept, sideMerged)
	}
	// Curated days newer than every commit leave nothing.
	future := []Entry{{Date: time.Now().AddDate(0, 0, 2).Format(DateLayout), Text: "x"}}
	if es, _ := Commits(dir, future); len(es) != 0 {
		t.Errorf("older than the newest day kept: %+v", es)
	}
	// List places the day's commits after its curated line.
	all := merge([]Entry{{Date: today, Text: "curated"}, {Date: "2000-01-01", Text: "old"}},
		[]Entry{{Date: "2010-01-01", Text: "c1", Commit: true}, {Date: today, Text: "c2", Commit: true}})
	var order []string
	for _, e := range all {
		order = append(order, e.Text)
	}
	if strings.Join(order, ",") != "curated,c2,c1,old" {
		t.Errorf("merge order %v", order)
	}
}

func TestRepo(t *testing.T) {
	defer func(f func() (string, error)) { executable = f }(executable)
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(d, "bin", "fleet")
	os.WriteFile(exe, nil, 0o755)
	executable = func() (string, error) { return exe, nil }
	if got := Repo(); got != "" {
		t.Errorf("no go.mod: %q", got)
	}
	os.WriteFile(filepath.Join(d, "go.mod"), []byte("module example.com/other\n"), 0o644)
	if got := Repo(); got != "" {
		t.Errorf("other module: %q", got)
	}
	os.WriteFile(filepath.Join(d, "go.mod"), []byte(module+"\n\ngo 1.26\n"), 0o644)
	want, _ := filepath.EvalSymlinks(d)
	if got := Repo(); got != want {
		t.Errorf("Repo() = %q, want %q", got, want)
	}
	// A symlink elsewhere resolves to the repo's binary.
	link := filepath.Join(t.TempDir(), "fleet")
	os.Symlink(exe, link)
	executable = func() (string, error) { return link, nil }
	if got := Repo(); got != want {
		t.Errorf("via symlink: %q", got)
	}
	// Not under bin/.
	other := filepath.Join(d, "fleet")
	os.WriteFile(other, nil, 0o755)
	executable = func() (string, error) { return other, nil }
	if got := Repo(); got != "" {
		t.Errorf("not in bin: %q", got)
	}
}

func TestVersion(t *testing.T) {
	if v := versionOf(nil); v != "fleet (unknown build)" {
		t.Errorf("%q", v)
	}
	at := time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC)
	v := versionOf([]debug.BuildSetting{{Key: "vcs.revision", Value: "66a2ece0123456789"},
		{Key: "vcs.time", Value: at.Format(time.RFC3339)}, {Key: "vcs.modified", Value: "true"}})
	if want := "fleet 66a2ece+dirty · built " + at.Local().Format("2006-01-02 15:04"); v != want {
		t.Errorf("%q want %q", v, want)
	}
	if Plain("use `fleet up` now") != "use fleet up now" {
		t.Error("Plain")
	}
}

func TestWithTag(t *testing.T) {
	for _, c := range []struct{ tag, build, want string }{
		{"", "fleet (unknown build)", "fleet (unknown build)"},
		{"v1.21.0+pro.fae2220", "fleet (unknown build)", "fleet v1.21.0+pro.fae2220"},
		{"v1.2.0", "fleet 66a2ece · built 2026-11-12 10:00", "fleet v1.2.0 (66a2ece · built 2026-11-12 10:00)"},
	} {
		if got := WithTag(c.tag, c.build); got != c.want {
			t.Errorf("WithTag(%q, %q) = %q, want %q", c.tag, c.build, got, c.want)
		}
	}
}
