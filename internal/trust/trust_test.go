package trust

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// claudeFile writes a config file with a couple of keys fleet must not lose.
// tmpHome points every default store at a temp home and clears the config
// overrides a tool may export, so a test run inside an agent pane (which has
// CLAUDE_CONFIG_DIR or VIBE_HOME set) cannot touch the user's real config.
func tmpHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("VIBE_HOME", "")
	t.Setenv("FLEET_HOME", filepath.Join(home, ".fleet"))
}

func claudeFile(t *testing.T, path string) {
	t.Helper()
	const seed = `{
  "numStartups": 7,
  "oauthAccount": {"emailAddress": "someone@example.com"},
  "projects": {
    "/some/other/dir": {"hasTrustDialogAccepted": true, "lastCost": 1.5}
  }
}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, body := splitComments(b)
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, b)
	}
	return m
}

// claudeTrusted reports whether every startup modal for dir is pre-answered
// in a claude config file.
func claudeTrusted(t *testing.T, path, dir string) bool {
	t.Helper()
	m := readJSON(t, path)
	p, _ := m["projects"].(map[string]any)
	e, _ := p[dir].(map[string]any)
	for _, k := range claudeKeys {
		if b, _ := e[k].(bool); !b {
			return false
		}
	}
	return true
}

func TestMarkClaudeKeepsEveryOtherKey(t *testing.T) {
	home := t.TempDir()
	tmpHome(t, home)
	path := filepath.Join(home, ".claude.json")
	claudeFile(t, path)

	dir := filepath.Join(home, "work", "repo")
	r, err := Mark("claude", nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Changed || r.File != path || r.Tool != "claude" {
		t.Fatalf("got %+v", r)
	}
	if !claudeTrusted(t, path, dir) {
		t.Fatalf("%s not trusted", dir)
	}

	m := readJSON(t, path)
	if m["numStartups"] != float64(7) {
		t.Errorf("numStartups lost: %v", m["numStartups"])
	}
	if acc, _ := m["oauthAccount"].(map[string]any); acc["emailAddress"] != "someone@example.com" {
		t.Errorf("oauthAccount lost: %v", m["oauthAccount"])
	}
	// The other project keeps its own keys, not just its trust flag.
	p, _ := m["projects"].(map[string]any)
	other, _ := p["/some/other/dir"].(map[string]any)
	if other["lastCost"] != 1.5 {
		t.Errorf("other project lost keys: %v", other)
	}

	// Already trusted: no second write.
	r2, err := Mark("claude", nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Changed {
		t.Error("second Mark rewrote an already trusted dir")
	}
}

func TestMarkClaudeUsesTheProfileConfigDir(t *testing.T) {
	home := t.TempDir()
	tmpHome(t, home)
	cfg := filepath.Join(home, ".claude-work")
	if err := os.Mkdir(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "repo")

	// The profile env is written as $HOME/... in tools.toml, like claude-work.
	if _, err := Mark("claude", map[string]string{"CLAUDE_CONFIG_DIR": "$HOME/.claude-work"}, dir); err != nil {
		t.Fatal(err)
	}
	if !claudeTrusted(t, filepath.Join(cfg, ".claude.json"), dir) {
		t.Error("not trusted in the profile's own config dir")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
		t.Error("wrote the default config file instead of the profile's")
	}
}

func TestMarkClaudeCreatesAMissingFile(t *testing.T) {
	home := t.TempDir()
	tmpHome(t, home)
	dir := filepath.Join(home, "repo")
	if _, err := Mark("claude", nil, dir); err != nil {
		t.Fatal(err)
	}
	if !claudeTrusted(t, filepath.Join(home, ".claude.json"), dir) {
		t.Error("not trusted")
	}
}

// A trust store is a security record: it must not be world-readable.
func TestMarkClaudeWritesOwnerOnly(t *testing.T) {
	home := t.TempDir()
	tmpHome(t, home)
	if _, err := Mark("claude", nil, filepath.Join(home, "repo")); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", st.Mode().Perm())
	}
}

// Claude rewrites the same file while fleet marks it. Each write must see
// the bytes on disk at that moment, so neither side's keys are lost.
func TestMarkClaudeMergesConcurrentWrites(t *testing.T) {
	home := t.TempDir()
	tmpHome(t, home)
	path := filepath.Join(home, ".claude.json")
	claudeFile(t, path)

	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := Mark("claude", nil, filepath.Join(home, fmt.Sprintf("repo-%d", i))); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if d := filepath.Join(home, fmt.Sprintf("repo-%d", i)); !claudeTrusted(t, path, d) {
			t.Errorf("%s lost by a concurrent write", d)
		}
	}
	if m := readJSON(t, path); m["numStartups"] != float64(7) {
		t.Errorf("numStartups lost: %v", m["numStartups"])
	}
}

func TestMarkCopilotKeepsTheCommentHeader(t *testing.T) {
	home := t.TempDir()
	tmpHome(t, home)
	cfg := filepath.Join(home, ".copilot")
	if err := os.Mkdir(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg, "config.json")
	const seed = `// User settings belong in settings.json.
// This file is managed automatically.
{
  "lastLoggedInUser": {"login": "someone"},
  "trustedFolders": ["/already/trusted"]
}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, "repo")
	if _, err := Mark("copilot", nil, dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "// User settings belong in settings.json.\n// This file is managed automatically.\n") {
		t.Errorf("comment header lost:\n%s", b)
	}
	m := readJSON(t, path)
	got, _ := m["trustedFolders"].([]any)
	if len(got) != 2 || got[0] != "/already/trusted" || got[1] != dir {
		t.Errorf("trustedFolders = %v", got)
	}
	if u, _ := m["lastLoggedInUser"].(map[string]any); u["login"] != "someone" {
		t.Errorf("other keys lost: %v", m)
	}

	r, err := Mark("copilot", nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Changed {
		t.Error("second Mark rewrote an already trusted folder")
	}
}

func TestMarkVibeTrustsAndUntrusts(t *testing.T) {
	home := t.TempDir()
	tmpHome(t, home)
	cfg := filepath.Join(home, ".vibe")
	if err := os.Mkdir(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg, "trusted_folders.toml")
	dir := filepath.Join(home, "repo")
	// tomli_w writes a non-empty array over several lines; the dir was
	// declined once, so it also has to come off `untrusted`.
	seed := fmt.Sprintf("trusted = [\n    %q,\n]\nuntrusted = [\n    %q,\n]\n", "/already/trusted", dir)
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Mark("vibe", nil, dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	trusted, err := tomlArray(string(b), "trusted")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(trusted, dir) || !contains(trusted, "/already/trusted") {
		t.Errorf("trusted = %v", trusted)
	}
	untrusted, err := tomlArray(string(b), "untrusted")
	if err != nil {
		t.Fatal(err)
	}
	if contains(untrusted, dir) {
		t.Errorf("still untrusted: %v", untrusted)
	}

	r, err := Mark("vibe", nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Changed {
		t.Error("second Mark rewrote an already trusted folder")
	}
}

func TestMarkVibeUsesVibeHome(t *testing.T) {
	home := t.TempDir()
	tmpHome(t, home)
	cfg := filepath.Join(home, "elsewhere")
	if err := os.Mkdir(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Mark("vibe", map[string]string{"VIBE_HOME": cfg}, filepath.Join(home, "repo")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg, "trusted_folders.toml")); err != nil {
		t.Error("VIBE_HOME ignored")
	}
}

// A half-written array must not read as "nothing is trusted", which would
// drop every folder the user had already trusted.
func TestMarkVibeRefusesABrokenFile(t *testing.T) {
	home := t.TempDir()
	tmpHome(t, home)
	cfg := filepath.Join(home, ".vibe")
	if err := os.Mkdir(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg, "trusted_folders.toml")
	if err := os.WriteFile(path, []byte("trusted = [\n    \"/a\",\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Mark("vibe", nil, filepath.Join(home, "repo")); err == nil {
		t.Fatal("want an error for an unterminated array")
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "/a") {
		t.Errorf("clobbered the broken file: %q", b)
	}
}

func TestMarkUnknownTool(t *testing.T) {
	home := t.TempDir()
	tmpHome(t, home)
	_, err := Mark("agy", nil, home)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
	if Supported("agy") {
		t.Error("agy reported as supported")
	}
	for _, c := range []string{"claude", "/home/me/.local/bin/claude", "copilot", "vibe"} {
		if !Supported(c) {
			t.Errorf("%s not supported", c)
		}
	}
}

func TestMarkRelativeDir(t *testing.T) {
	home := t.TempDir()
	tmpHome(t, home)
	if _, err := Mark("claude", nil, "repo"); err == nil {
		t.Fatal("want an error for a relative dir")
	}
}

// A tool that has never run has no config directory, so there is nothing to
// pre-answer; that must be an error the caller can log, not a new file tree.
func TestMarkMissingConfigDir(t *testing.T) {
	home := t.TempDir()
	tmpHome(t, home)
	if _, err := Mark("vibe", nil, filepath.Join(home, "repo")); err == nil {
		t.Fatal("want an error when ~/.vibe does not exist")
	}
}

// A bracket inside a trusted path must not be read as the end of the array:
// a short read there would drop every folder the user already trusts.
// Anything vibe writes besides the two arrays has to survive too.
func TestMarkVibeKeepsOddPathsAndOtherKeys(t *testing.T) {
	home := t.TempDir()
	tmpHome(t, home)
	cfg := filepath.Join(home, ".vibe")
	if err := os.Mkdir(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg, "trusted_folders.toml")
	odd := "/srv/odd]name"
	seed := fmt.Sprintf("# vibe\nversion = 2\ntrusted = [\n    %q,\n    %q,\n]\n", odd, "/plain")
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "repo")
	if _, err := Mark("vibe", nil, dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	trusted, err := tomlArray(string(b), "trusted")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{odd, "/plain", dir} {
		if !contains(trusted, want) {
			t.Errorf("trusted = %v, want %q in it", trusted, want)
		}
	}
	for _, want := range []string{"# vibe", "version = 2"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("dropped %q:\n%s", want, b)
		}
	}
}

// A trustedFolders that is not a list of folders is someone else's config,
// not an empty one: report it instead of overwriting it.
func TestMarkCopilotRefusesAStrangeList(t *testing.T) {
	home := t.TempDir()
	tmpHome(t, home)
	cfg := filepath.Join(home, ".copilot")
	if err := os.Mkdir(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg, "config.json")
	seed := `{"trustedFolders": {"/a": true}}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Mark("copilot", nil, filepath.Join(home, "repo")); err == nil {
		t.Fatal("want an error for a trustedFolders that is not a list")
	}
	b, _ := os.ReadFile(path)
	if string(b) != seed {
		t.Errorf("rewrote it anyway: %s", b)
	}
}
