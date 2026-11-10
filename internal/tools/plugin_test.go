package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestWritePlugin(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plugins", "a1")
	pl := Plugin{Exe: "/bin/fleet", Name: "a1", MCP: true}
	if err := WritePlugin(dir, pl); err != nil {
		t.Fatal(err)
	}
	var meta struct{ Name, Version string }
	b, _ := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	if json.Unmarshal(b, &meta); meta.Name != PluginName || meta.Version != PluginVersion {
		t.Errorf("plugin.json %s", b)
	}
	var hooks struct {
		Hooks map[string][]struct{ Hooks []struct{ Type, Command string } }
	}
	b, _ = os.ReadFile(filepath.Join(dir, "hooks", "hooks.json"))
	if err := json.Unmarshal(b, &hooks); err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{"Stop", "SubagentStart", "SubagentStop", "PermissionRequest", "SessionEnd"} {
		h := hooks.Hooks[e]
		if len(h) != 1 || len(h[0].Hooks) != 1 || h[0].Hooks[0].Command != "/bin/fleet hook "+strings.ToLower(e)+" --agent a1" {
			t.Errorf("%s hook %+v", e, h)
		}
	}
	b, _ = os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if cp, _ := PeerMCP("/bin/fleet", "a1"); !jsonEqual(t, b, []byte(cp)) {
		t.Errorf(".mcp.json %s want %s", b, cp)
	}

	// Rewriting the same plugin touches nothing; dropping MCP removes
	// .mcp.json and NoStatus reaches every hook.
	st, _ := os.Stat(filepath.Join(dir, "hooks", "hooks.json"))
	if err := WritePlugin(dir, pl); err != nil {
		t.Fatal(err)
	}
	if st2, _ := os.Stat(filepath.Join(dir, "hooks", "hooks.json")); !os.SameFile(st, st2) {
		t.Error("unchanged hooks.json rewritten")
	}
	if err := WritePlugin(dir, Plugin{Exe: "/bin/fleet", Name: "a1", NoStatus: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".mcp.json")); !os.IsNotExist(err) {
		t.Errorf(".mcp.json kept: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "hooks", "hooks.json")); strings.Count(string(b), "--no-status") != len(pluginEvents) {
		t.Errorf("NoStatus hooks %s", b)
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return string(ja) == string(jb)
}

func TestPluginArgv(t *testing.T) {
	d, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := Vars{Fleet: "/bin/fleet", Name: "a1"}
	base.PeerMCP, base.PeerMCPClaude = PeerMCP("/bin/fleet", "a1")
	base.Hooks = ClaudeHooks("/bin/fleet", "a1")

	// Copilot: fleet-peer moves into the plugin, the flag goes.
	cp := d["copilot"]
	v := base.WithPlugin("/p/a1", cp)
	argv, _ := cp.Argv(v, Launch{})
	if !containsRun(argv, []string{"--plugin-dir", "/p/a1"}) || slices.Contains(argv, "--additional-mcp-config") {
		t.Errorf("copilot argv %q", argv)
	}
	if acp := cp.ACPArgv(v, Launch{}); !containsRun(acp, []string{"--plugin-dir", "/p/a1"}) || slices.Contains(acp, "--additional-mcp-config") {
		t.Errorf("copilot acp argv %q", acp)
	}
	if !cp.UsesPlugin(false) || !cp.UsesPlugin(true) || d["fake"].UsesPlugin(false) {
		t.Error("UsesPlugin")
	}

	// Claude in tmux: keeps --mcp-config, --settings carries the fence only.
	cl := d["claude"]
	v = base.WithPlugin("/p/a1", cl).Fenced("/r.wt")
	argv, _ = cl.Argv(v, Launch{})
	if !containsRun(argv, []string{"--plugin-dir", "/p/a1"}) || !slices.Contains(argv, "--mcp-config") {
		t.Errorf("claude argv %q", argv)
	}
	if i := slices.Index(argv, "--settings"); i < 0 || argv[i+1] != ClaudeFence("/r.wt") || strings.Contains(argv[i+1], "hook") {
		t.Errorf("claude --settings %q", argv)
	}
	if argv, _ := cl.Argv(base.WithPlugin("/p/a1", cl).Fenced(""), Launch{}); slices.Contains(argv, "--settings") {
		t.Errorf("unfenced claude with plugin still passes --settings: %q", argv)
	}

	// Claude over ACP: plugin and fence in session/new _meta.
	want := `{"claudeCode":{"options":{"plugins":[{"path":"/p/a1","type":"local"}],"settings":` + ClaudeFence("/r.wt") + `}}}`
	if got := cl.SessionMeta(Vars{Plugin: "/p/a1", Hooks: ClaudeFence("/r.wt")}); got != want {
		t.Errorf("SessionMeta %s\nwant %s", got, want)
	}
	if got := cp.SessionMeta(v); got != "" {
		t.Errorf("copilot SessionMeta %s", got)
	}
}

func containsRun(l, run []string) bool {
	for i := 0; i+len(run) <= len(l); i++ {
		if slices.Equal(l[i:i+len(run)], run) {
			return true
		}
	}
	return false
}
