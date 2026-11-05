package prefs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/summary"
)

func loadWith(t *testing.T, body string) *Prefs {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if body != "" {
		os.WriteFile(p, []byte(body), 0o644)
	}
	t.Setenv("FLEET_CONFIG", p)
	pr, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return pr
}

func TestSummaryDefaultsToHaiku(t *testing.T) {
	for _, pr := range []*Prefs{loadWith(t, ""), nil} {
		s := pr.Summary()
		if !s.Enabled || s.Model != summary.DefaultModel || s.Command != summary.DefaultCommand {
			t.Fatalf("defaults = %+v", s)
		}
		if s.MinInterval != summary.DefaultMinInterval || s.Timeout != summary.DefaultTimeout || len(s.Args) == 0 {
			t.Fatalf("defaults = %+v", s)
		}
	}
}

func TestSummarySection(t *testing.T) {
	pr := loadWith(t, `[summary]
model = "claude-haiku-5.5-fast"
command = "claude"
args = ["-p", "{prompt}", "--model", "{model}"]
min_interval = "2m"
timeout = "20s"
`)
	s := pr.Summary()
	if s.Model != "claude-haiku-5.5-fast" || s.Command != "claude" {
		t.Fatalf("s = %+v", s)
	}
	if s.MinInterval != 2*time.Minute || s.Timeout != 20*time.Second || len(s.Args) != 4 {
		t.Fatalf("s = %+v", s)
	}
	if off := loadWith(t, "[summary]\nenabled = false\n").Summary(); off.Enabled {
		t.Fatal("enabled = false ignored")
	}
}
