package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaudeDesktopConfig(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, tc := range []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"darwin", nil, "/h/Library/Application Support/Claude/claude_desktop_config.json"},
		{"windows", map[string]string{"APPDATA": "/appdata"}, "/appdata/Claude/claude_desktop_config.json"},
		{"linux", nil, "/h/.config/Claude/claude_desktop_config.json"},
		{"linux", map[string]string{"XDG_CONFIG_HOME": "/x"}, "/x/Claude/claude_desktop_config.json"},
	} {
		got, err := claudeDesktopConfig(tc.goos, "/h", env(tc.env))
		if err != nil || got != tc.want {
			t.Errorf("%s %v: %q %v, want %q", tc.goos, tc.env, got, err, tc.want)
		}
	}
	if _, err := claudeDesktopConfig("windows", "/h", env(nil)); err == nil {
		t.Error("windows without APPDATA: want an error")
	}
}

func TestMergeDesktop(t *testing.T) {
	entry := desktopEntry("/opt/fleet", "desktop", "/usr/bin", func(k string) string {
		return map[string]string{"FLEET_HOME": "/fh"}[k]
	})
	const other = `{"globalShortcut":"Ctrl+Space","mcpServers":{"git":{"command":"git-mcp"},"fleet":{"command":"old"}}}`
	for _, tc := range []struct {
		name, in      string
		entry         map[string]any
		changed, gone bool
		err           string
	}{
		{name: "empty file", in: "", entry: entry, changed: true},
		{name: "replace, keep others", in: other, entry: entry, changed: true},
		{name: "remove, keep others", in: other, changed: true, gone: true},
		{name: "remove absent", in: `{"mcpServers":{}}`, gone: true},
		{name: "bad json", in: `{`, entry: entry, err: "not valid JSON"},
		{name: "bad servers", in: `{"mcpServers":[]}`, entry: entry, err: "not an object"},
	} {
		out, changed, err := mergeDesktop([]byte(tc.in), tc.entry)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: err %v, want %q", tc.name, err, tc.err)
			}
			continue
		}
		if err != nil || changed != tc.changed {
			t.Fatalf("%s: changed %v err %v", tc.name, changed, err)
		}
		var cfg struct {
			Shortcut string                    `json:"globalShortcut"`
			Servers  map[string]map[string]any `json:"mcpServers"`
		}
		if err := json.Unmarshal(out, &cfg); err != nil {
			t.Fatalf("%s: output %q: %v", tc.name, out, err)
		}
		if tc.in == other && (cfg.Shortcut != "Ctrl+Space" || cfg.Servers["git"]["command"] != "git-mcp") {
			t.Errorf("%s: lost the user's settings: %s", tc.name, out)
		}
		fl, has := cfg.Servers["fleet"]
		if has == tc.gone {
			t.Errorf("%s: fleet present %v: %s", tc.name, has, out)
		}
		if has && (fl["command"] != "/opt/fleet" || !strings.Contains(string(out), `"--admiral",`) || !strings.Contains(string(out), `"FLEET_HOME": "/fh"`)) {
			t.Errorf("%s: entry %s", tc.name, out)
		}
	}
}

func TestDesktopPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	for _, tc := range []struct{ path, dir, want string }{
		{"/usr/bin" + sep + "/bin", "/usr/bin", "/usr/bin" + sep + "/bin"},
		{"/usr/bin", "/opt/homebrew/bin", "/opt/homebrew/bin" + sep + "/usr/bin"},
		{"", "/usr/bin", "/usr/bin"},
	} {
		if got := desktopPath(tc.path, tc.dir); got != tc.want {
			t.Errorf("desktopPath(%q, %q) = %q, want %q", tc.path, tc.dir, got, tc.want)
		}
	}
}

func TestWriteDesktopBacksUp(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(p, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	backup, err := writeDesktop(p, []byte("new"), time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(backup); string(b) != "old" || !strings.HasSuffix(backup, ".fleet-backup-20261117-180000") {
		t.Errorf("backup %s: %q", backup, b)
	}
	if b, _ := os.ReadFile(p); string(b) != "new" {
		t.Errorf("config %q", b)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o640 {
		t.Errorf("mode %v, want the old 0640", st.Mode().Perm())
	}
}

func TestWriteDesktopKeepsEveryBackup(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	now := time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC)
	var backups []string
	for _, v := range []string{"v1", "v2", "v3"} {
		if err := os.WriteFile(p, []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
		b, err := writeDesktop(p, []byte("next"), now)
		if err != nil {
			t.Fatal(err)
		}
		backups = append(backups, b)
	}
	for i, v := range []string{"v1", "v2", "v3"} {
		if got, _ := os.ReadFile(backups[i]); string(got) != v {
			t.Errorf("backup %s holds %q, want %q", backups[i], got, v)
		}
	}
}

func TestParseConnect(t *testing.T) {
	cases := []struct {
		args         []string
		ok           bool
		name, config string
		print, rm    bool
	}{
		{[]string{"claude-desktop"}, true, desktopAdmiral, "", false, false},
		{[]string{"claude-desktop", "--print", "--name", "x"}, true, "x", "", true, false},
		{[]string{"--remove", "claude-desktop", "--print"}, true, desktopAdmiral, "", true, true},
		{[]string{"claude-desktop", "--remove", "--config", "/c.json"}, true, desktopAdmiral, "/c.json", false, true},
		{[]string{"--dry-run", "claude-desktop"}, true, desktopAdmiral, "", true, false},
		{[]string{"cursor"}, false, "", "", false, false},
		{[]string{"claude-desktop", "extra"}, false, "", "", false, false},
		{nil, false, "", "", false, false},
	}
	for _, c := range cases {
		o, err := parseConnect(c.args)
		if (err == nil) != c.ok {
			t.Errorf("%v: err = %v, want ok=%v", c.args, err, c.ok)
			continue
		}
		if c.ok && (o.name != c.name || o.config != c.config || o.print != c.print || o.remove != c.rm) {
			t.Errorf("%v: got %+v", c.args, o)
		}
	}
}
