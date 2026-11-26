package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if pr := AskPrompt(a, "work"); !strings.Contains(pr, "artifact ask reply team-floorplan-c4-long-name 0123456789ab") || !strings.Contains(pr, "what is S3?") {
		t.Errorf("prompt lacks the reply command or the question:\n%s", pr)
	}
}
