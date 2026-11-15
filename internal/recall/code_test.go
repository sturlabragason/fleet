package recall

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// Touched names the agent behind each commit to a file (a Fleet-Agent
// trailer first, then a merge subject, a fleet/ branch tip, a claimed ADR),
// with its last report, leaves a commit with none unnamed and commits to
// other files out.
func TestTouched(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	repo := t.TempDir()
	write := func(f, s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, f), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, repo, "init", "-q", "-b", "main")
	write("a.go", "package a\n")
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-qm", "seed")
	gitT(t, repo, "checkout", "-qb", "fleet/x")
	write("a.go", "package a\n\nfunc A() {}\n")
	gitT(t, repo, "commit", "-qam", "feat: A")
	gitT(t, repo, "checkout", "-q", "main")
	gitT(t, repo, "merge", "-q", "--no-ff", "-m", "Merge fleet/x: add A", "fleet/x")
	write("b.go", "package a\n")
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-qm", "only b")
	write("a.go", "package a\n\nfunc A() { _ = 1 }\n")
	gitT(t, repo, "commit", "-qam", "fix: A by ADR (ADR-7)")
	gitT(t, repo, "checkout", "-qb", "fleet/z")
	write("a.go", "package a\n\nfunc A() { _ = 2 }\n")
	gitT(t, repo, "commit", "-qam", "feat: A fast-forwarded")
	gitT(t, repo, "checkout", "-q", "main")
	gitT(t, repo, "merge", "-q", "--ff-only", "fleet/z")
	gitT(t, repo, "checkout", "-qb", "fleet/w")
	write("a.go", "package a\n\nfunc A() { _ = 3 }\n")
	gitT(t, repo, "commit", "-qam", "fix: A trailer (ADR-7)\n\nFleet-Agent: x")
	gitT(t, repo, "checkout", "-q", "main")
	gitT(t, repo, "merge", "-q", "--ff-only", "fleet/w")

	o := Owners{
		Reports: []Msg{{From: "x", Text: "done: old"}, {From: "y", Text: "done: other"}, {From: "x", Text: "done: shipped A\ndetail"}},
		ADR:     map[int]string{7: "y"},
	}
	hits, err := Touched(t.Context(), repo, []string{"a.go"}, o, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ kind, who, title, text string }{
		{"commit", "x", "fix: A trailer (ADR-7)", "done: shipped A"},
		{"commit", "z", "feat: A fast-forwarded", ""},
		{"commit", "y", "fix: A by ADR (ADR-7)", "done: other"},
		{"merge", "x", "add A", "done: shipped A"},
		{"commit", "", "seed", ""},
	}
	if len(hits) != len(want) {
		t.Fatalf("hits = %+v, want %d", hits, len(want))
	}
	for i, w := range want {
		if h := hits[i]; h.Kind != w.kind || h.Who != w.who || h.Title != w.title || h.Text != w.text {
			t.Errorf("hit %d = %+v, want %+v", i, h, w)
		}
	}
	r := Result{Topic: "a.go", CodeFiles: []string{"a.go"}, Code: hits, noCass: true}
	if out := r.Format(); !strings.Contains(out, "## code: 5 commits to a.go") || !strings.Contains(out, "merge  x: add A") {
		t.Errorf("format:\n%s", out)
	}
	if _, err := Touched(t.Context(), t.TempDir(), []string{"a.go"}, Owners{}, 10); err == nil {
		t.Error("not a repo: want an error")
	}
}

// fleetAgent reads the Fleet-Agent trailer values git prints (ADR-137).
func TestFleetAgent(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"fu-trailer", "fu-trailer"},
		{"  a1 ", "a1"},
		{"first\x1esecond", "first"},
		{"\x1elater", "later"},
	} {
		if got := fleetAgent(c.in); got != c.want {
			t.Errorf("fleetAgent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
