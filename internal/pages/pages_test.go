package pages

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }

// fakeArtifact points the companion at a script that lists the given pages
// and serves dir/<page>.json as the peeked answer (exit 3 when absent).
func fakeArtifact(t *testing.T, pages ...string) string {
	t.Helper()
	dir := t.TempDir()
	list := ""
	for _, p := range pages {
		list += p + "\tprivate\t2026-11-18T20:00:00Z\thttps://example.invalid/" + p + "/\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "list"), []byte(list), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "artifact")
	script := "#!/bin/sh\nd=" + dir + "\n" +
		`case "$1" in list) cat "$d/list" ;; answer) [ -f "$d/$2.json" ] || exit 3; cat "$d/$2.json" ;; *) exit 1 ;; esac` + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_ARTIFACT", bin)
	t.Setenv("FLEET_CONFIG", filepath.Join(dir, "none.toml"))
	return dir
}

func answer(t *testing.T, dir, page string, at time.Time) {
	t.Helper()
	js := fmt.Sprintf(`{"page":%q,"decision":%q,"line":"%s: style A","t":%d}`, page, page+"-1", page, at.Unix())
	if err := os.WriteFile(filepath.Join(dir, page+".json"), []byte(js), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPoll(t *testing.T) {
	now := time.Now()
	home := t.TempDir()
	dir := fakeArtifact(t, "decide-old", "decide-new", "viz-chart", "blog-draft")
	answer(t, dir, "decide-old", now.Add(-time.Hour))
	answer(t, dir, "blog-draft", now.Add(-time.Hour)) // not a decision page

	poll := func(at time.Time) []string {
		t.Helper()
		as, err := Poll(t.Context(), home, at)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, a := range as {
			got = append(got, a.Page)
		}
		return got
	}
	if got := poll(now); len(got) != 0 {
		t.Fatalf("first poll handed on %v; it only records what was there", got)
	}

	answer(t, dir, "decide-new", now.Add(-10*time.Second))
	if got := poll(now); len(got) != 0 {
		t.Fatalf("answer inside the grace handed on: %v", got)
	}
	steps := []struct {
		name string
		at   time.Time
		want string
	}{
		{"untaken past the grace", now.Add(Grace), "decide-new"},
		{"handed on once", now.Add(2 * Grace), ""},
	}
	for _, s := range steps {
		got := poll(s.at)
		if (s.want == "" && len(got) != 0) || (s.want != "" && (len(got) != 1 || got[0] != s.want)) {
			t.Errorf("%s: got %v, want %q", s.name, got, s.want)
		}
	}

	answer(t, dir, "viz-chart", now)
	if got := poll(now.Add(3 * Grace)); len(got) != 1 || got[0] != "viz-chart" {
		t.Errorf("viz page answer: got %v", got)
	}
}
