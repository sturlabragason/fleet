package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/tools"
)

// safeDir moves a home-directory spawn into the agent's own scratch dir and
// leaves every other dir alone (ADR-47).
func TestSafeDirRedirectsHomeAndRoot(t *testing.T) {
	f := &Fleet{Home: t.TempDir()}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	want := filepath.Join(f.Home, ScratchRoot, "a1")
	for _, dir := range []string{home, home + "/", "/"} {
		got, moved := f.safeDir("a1", dir)
		if !moved || got != want {
			t.Errorf("%s: got %q moved=%v want %q", dir, got, moved, want)
		}
		if st, err := os.Stat(got); err != nil || !st.IsDir() {
			t.Errorf("%s: scratch dir not created: %v", dir, err)
		}
	}
	keep := t.TempDir()
	if got, moved := f.safeDir("a1", keep); moved || got != keep {
		t.Errorf("ordinary dir: got %q moved=%v", got, moved)
	}
	if !f.InScratch(want) || f.InScratch(keep) {
		t.Error("InScratch")
	}
	if !strings.Contains(RedirectNote(), "home directory") {
		t.Errorf("note: %q", RedirectNote())
	}
}

// A dir inside the home directory is a normal project dir; only $HOME itself
// is out of bounds.
func TestSafeDirKeepsDirsUnderHome(t *testing.T) {
	f := &Fleet{Home: t.TempDir()}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	dir := filepath.Join(home, "git", "project")
	if got, moved := f.safeDir("a1", dir); moved || got != dir {
		t.Errorf("got %q moved=%v", got, moved)
	}
}

// TrustDir writes the tool's own config, and auto_trust = false stops it.
func TestTrustDirHonoursPref(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".claude")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	p := tools.Profile{Name: "claude-test", Command: "claude", Env: map[string]string{"CLAUDE_CONFIG_DIR": cfg}}
	dir := t.TempDir()

	// No config file: auto_trust defaults to on.
	f := &Fleet{Home: t.TempDir(), Profiles: map[string]tools.Profile{p.Name: p}}
	f.TrustDir(p.Name, p, dir)
	b, err := os.ReadFile(filepath.Join(cfg, ".claude.json"))
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(b), dir) || !strings.Contains(string(b), "hasTrustDialogAccepted") {
		t.Errorf("config: %s", b)
	}

	off := t.TempDir()
	f.Prefs = loadPrefs(t, "[profile.claude-test]\nauto_trust = false\n")
	f.TrustDir(p.Name, p, off)
	b, _ = os.ReadFile(filepath.Join(cfg, ".claude.json"))
	if strings.Contains(string(b), off) {
		t.Errorf("auto_trust = false still wrote the config: %s", b)
	}
}

// A tool with no trust store (agy) must not fail a spawn.
func TestTrustDirSurvivesUnsupportedTool(t *testing.T) {
	p := tools.Profile{Name: "agy", Command: "agy"}
	f := &Fleet{Home: t.TempDir(), Profiles: map[string]tools.Profile{p.Name: p}}
	f.TrustDir(p.Name, p, t.TempDir()) // must not panic
}

// The on-screen fallback picks "Yes, I trust this folder" even though the
// dialog is unnumbered and puts "No, exit" first.
func TestAnswerTrustPicksYes(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "status", "testdata", "claude", "waiting-trust.txt"))
	if err != nil {
		t.Fatal(err)
	}
	f := testFleet(t)
	prof := tools.Profile{Name: "claude", Patterns: "claude"}
	pr := prefs.Neutral()

	p := &fakePane{screens: []string{"", string(b)}}
	if !f.answerTrust(p, p.sleep, "%1", prof, pr) {
		t.Fatal("dialog not answered")
	}
	// From "No, exit" (1) down to "Yes, I trust this folder" (2).
	if got := strings.Join(p.keys, " "); got != "Down Enter" {
		t.Errorf("keys %q", got)
	}

	// No dialog: give up as soon as the input box is there, pressing nothing.
	p = &fakePane{screens: []string{"", "❯ "}}
	if f.answerTrust(p, p.sleep, "%1", prof, pr) || len(p.keys) > 0 {
		t.Errorf("answered without a dialog: %q", p.keys)
	}
	if p.slept > trustWindow {
		t.Errorf("waited too long: %v", p.slept)
	}

	// A tool whose patterns never mention trust is not watched at all: the
	// watch costs a spawn real seconds while a slow tool draws its box.
	p = &fakePane{screens: []string{""}}
	if f.answerTrust(p, p.sleep, "%1", tools.Profile{Name: "fake", Patterns: "fake"}, pr) || p.captures != 0 {
		t.Errorf("watched a tool that never asks: %d captures", p.captures)
	}

	// Claude's second startup modal is the same kind of question, and stops
	// a spawn just as hard (ADR-47).
	imports := strings.Join([]string{
		"  Allow external CLAUDE.md file imports?",
		"  Imports /home/u/notes/core.md",
		"  ❯ No, disable external imports",
		"    Yes, allow external imports",
		"",
		"  Enter to confirm · Esc to cancel",
	}, "\n")
	p = &fakePane{screens: []string{imports}}
	if !f.answerTrust(p, p.sleep, "%1", prof, pr) {
		t.Fatal("external imports dialog not answered")
	}
	if got := strings.Join(p.keys, " "); got != "Down Enter" {
		t.Errorf("imports keys %q", got)
	}

	// agy has no trust store to pre-mark, so the on-screen answer is the
	// only thing that gets it past its dialog (ADR-47).
	ab, err := os.ReadFile(filepath.Join("..", "status", "testdata", "agy", "waiting-trust.txt"))
	if err != nil {
		t.Fatal(err)
	}
	p = &fakePane{screens: []string{"", string(ab)}}
	if !f.answerTrust(p, p.sleep, "%1", tools.Profile{Name: "agy", Patterns: "agy"}, pr) {
		t.Fatal("agy dialog not answered")
	}
	// "Yes, I trust this folder" is already option 1: just confirm.
	if got := strings.Join(p.keys, " "); got != "Enter" {
		t.Errorf("agy keys %q", got)
	}

	// auto_trust off: the user answers it by hand.
	pr.AutoTrust = false
	p = &fakePane{screens: []string{string(b)}}
	if f.answerTrust(p, p.sleep, "%1", prof, pr) || p.captures != 0 {
		t.Error("auto_trust = false should not touch the pane")
	}
}

// loadPrefs reads body as the user's config file for this test only.
func loadPrefs(t *testing.T, body string) *prefs.Prefs {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_CONFIG", path)
	p, err := prefs.Load()
	if err != nil {
		t.Fatal(err)
	}
	return p
}
