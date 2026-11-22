package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRules(t *testing.T) {
	for _, c := range []struct {
		name  string
		rules int
		want  string // "" = no line
	}{
		{"none", 0, ""},
		{"small", 2, levelOK},
		{"over the cap", 30, levelWarn},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			if c.rules > 0 {
				body := "## Standing user rules\n\n" + strings.Repeat("- "+strings.Repeat("x", 200)+"\n", c.rules)
				if err := os.WriteFile(filepath.Join(home, "worker.md"), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, ok := checkRules(home)
			if c.want == "" && ok || c.want != "" && got.Level != c.want {
				t.Fatalf("checkRules = %+v, %v; want level %q", got, ok, c.want)
			}
		})
	}
}

func TestCheckRTK(t *testing.T) {
	have := func(string) error { return nil }
	for _, c := range []struct {
		name          string
		look          func(string) error
		claude, hooks string
		want          string // "" = no line
	}{
		{"not installed", func(string) error { return errors.New("no") }, "", "", ""},
		{"no hooks", have, "{}", "", levelInfo},
		{"claude only", have, `{"hooks":"rtk-rewrite.sh"}`, "", levelInfo},
		{"both", have, `{"hooks":"rtk-rewrite.sh"}`, `{"bash":"rtk hook copilot"}`, levelOK},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			write := func(p, s string) {
				t.Helper()
				if s == "" {
					return
				}
				if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(home, ".claude", "settings.json"), c.claude)
			write(filepath.Join(home, ".copilot", "hooks", "rtk.json"), c.hooks)
			got, ok := checkRTK(home, c.look)
			if c.want == "" && ok || c.want != "" && got.Level != c.want {
				t.Fatalf("checkRTK = %+v, %v; want level %q", got, ok, c.want)
			}
		})
	}
}
