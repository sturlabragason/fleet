package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/pages"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/tools"
)

func TestAskRouting(t *testing.T) {
	repo := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "config.toml")
	body := "[integrations.task.contexts]\nwork = \"" + filepath.Join(repo, "missing") + ", " + repo + "\"\n" +
		"[integrations.artifact.ask]\nwork = \"claude-copilot\"\npersonal = \"nope\"\ndefault = \"claude\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_CONFIG", cfg)
	p, err := prefs.Load()
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]tools.Profile{"claude-copilot": {}, "claude": {}}
	home := t.TempDir()
	for _, tc := range []struct{ ctx, dir, profile string }{
		{"work", repo, "claude-copilot"},
		{"personal", filepath.Join(home, "asks"), "claude"}, // unknown profile, no dirs
		{"", filepath.Join(home, "asks"), "claude"},
	} {
		if got := askDir(p, tc.ctx, home); got != tc.dir {
			t.Errorf("askDir(%q) = %q, want %q", tc.ctx, got, tc.dir)
		}
		if got := askProfile(p, tc.ctx, "fallback", have); got != tc.profile {
			t.Errorf("askProfile(%q) = %q, want %q", tc.ctx, got, tc.profile)
		}
	}
	a := pages.Ask{Page: "team-floorplan-c4-long-name", ID: "0123456789ab", Question: "what is S3?"}
	if n := askName(a); n != "ask-team-floorplan-c4-lo-012345" || !validName(n) {
		t.Errorf("askName = %q (valid %v)", n, validName(n))
	}
	if pr := AskPrompt(a, "work", "/x/page.html"); !strings.Contains(pr, "/x/page.html") || !strings.Contains(pr, "what is S3?") || strings.Contains(pr, "artifact ask reply") {
		t.Errorf("prompt lacks the page or the question, or asks for a shell command:\n%s", pr)
	}
}

// An ask agent's first done, blocked or question report is posted as the
// answer, once; progress is not, and nor is any other agent's report.
func TestAnswerFromReport(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "artifact")
	script := "#!/bin/sh\n[ \"$1 $2\" = \"ask reply\" ] || exit 1\necho \"$3 $4 $5\" >> " + filepath.Join(dir, "replies") + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_ARTIFACT", bin)
	t.Setenv("FLEET_CONFIG", filepath.Join(dir, "none.toml"))
	f := &Fleet{Home: t.TempDir()}
	a := pages.Ask{Page: "floor-a", ID: "aaaaaaaaaaaa", Question: "why?"}
	d := askFiles(f.Home, "ask-floor-a-aaaaaa")
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	rec, _ := json.Marshal(a)
	if err := os.WriteFile(filepath.Join(d, "ask.json"), rec, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, r := range [][2]string{{"someone-else", "done"}, {"ask-floor-a-aaaaaa", "progress"}, {"ask-floor-a-aaaaaa", "done"}, {"ask-floor-a-aaaaaa", "done"}} {
		if err := f.answerFromReport(t.Context(), r[0], r[1], "because.\n"); err != nil {
			t.Fatalf("%v: %v", r, err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "replies"))
	if got := strings.TrimSpace(string(b)); got != "floor-a aaaaaaaaaaaa because." {
		t.Errorf("replies = %q", got)
	}
	if _, err := os.Stat(d); !os.IsNotExist(err) {
		t.Errorf("ask files left: %v", err)
	}
	// A failed post fails the report and keeps the record for the next one.
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "ask.json"), rec, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_ARTIFACT", filepath.Join(dir, "missing"))
	if err := f.answerFromReport(t.Context(), "ask-floor-a-aaaaaa", "done", "x"); err == nil {
		t.Error("failed post: no error")
	}
	if _, err := os.Stat(filepath.Join(d, "ask.json")); err != nil {
		t.Errorf("record not restored: %v", err)
	}
}

func TestReadOnlyLaunch(t *testing.T) {
	ro := &board.Card{May: []string{ReadOnly}}
	can := tools.Profile{ReadOnlyArgs: []string{"--deny"}}
	adapter := tools.Profile{ReadOnlyArgs: []string{"--deny"}, ACPCommand: []string{"npx"}}
	for _, c := range []struct {
		p       tools.Profile
		card    *board.Card
		acp     bool
		want    bool
		wantErr bool
	}{
		{can, ro, false, true, false},
		{can, nil, false, false, false},
		{tools.Profile{}, ro, false, false, true},
		{adapter, ro, true, false, true},
		{adapter, ro, false, true, false},
	} {
		got, err := readOnlyLaunch("a", c.p, c.card, c.acp)
		if got != c.want || (err != nil) != c.wantErr {
			t.Errorf("readOnlyLaunch(%+v, acp=%v) = %v, %v", c.p, c.acp, got, err)
		}
	}
}
