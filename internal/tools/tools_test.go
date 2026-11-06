package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/conf"
)

func TestDefaultsAndArgv(t *testing.T) {
	ps, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		profile string
		resume  bool
		id      string
		want    []string
		patt    string
	}{
		{"copilot", false, "U", []string{"copilot", "--session-id", "U"}, "copilot"},
		{"copilot", true, "U", []string{"copilot", "--resume", "U"}, "copilot"},
		{"claude", false, "U", []string{"claude", "--model", "opus", "--session-id", "U"}, "claude"},
		{"agy", true, "C", []string{"agy", "--conversation", "C"}, "agy"},
		{"agy", false, "C", []string{"agy"}, "agy"},
		{"vibe", true, "V", []string{"vibe", "--resume", "V"}, "vibe"},
		{"fake", false, "", []string{"/bin/fleet", "fakeagent", "a1"}, "fake"},
	}
	for _, c := range cases {
		p := ps[c.profile]
		got, _ := p.Argv(Vars{Fleet: "/bin/fleet", Name: "a1", ID: c.id}, Launch{Resume: c.resume})
		if !reflect.DeepEqual(got, c.want) || p.Patterns != c.patt {
			t.Errorf("%s resume=%v: %v (%s) want %v", c.profile, c.resume, got, p.Patterns, c.want)
		}
	}
}

func TestExampleFile(t *testing.T) {
	b, err := os.ReadFile("../../examples/tools.toml")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tools.toml"), b, 0o644)
	ps, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := ps["claude-work"].Argv(Vars{ID: "U"}, Launch{}); !reflect.DeepEqual(got, []string{"claude", "--model", "opus", "--session-id", "U"}) {
		t.Errorf("claude-work: %v", got)
	}
	t.Setenv("HOME", "/h")
	if env := ps["claude-personal"].EnvList(); !slices.Contains(env, "CLAUDE_CONFIG_DIR=/h/.claude-personal") {
		t.Errorf("env not expanded: %v", env)
	}
}

func TestUserOverride(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tools.toml"), []byte("[copilot]\nargs = [\"--x\"]\n[mine]\ncommand = \"foo\"\npatterns = \"claude\"\n"), 0o644)
	ps, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := ps["copilot"].Argv(Vars{}, Launch{}); !reflect.DeepEqual(got, []string{"copilot", "--x"}) {
		t.Errorf("override: %v", got)
	}
	if ps["mine"].Command != "foo" || ps["mine"].Patterns != "claude" {
		t.Errorf("new profile: %+v", ps["mine"])
	}
}

func TestShellCommand(t *testing.T) {
	got := ShellCommand([]string{"copilot", "it's"})
	if !strings.HasPrefix(got, "bash -lc ") || !strings.Contains(got, "exec copilot") {
		t.Error(got)
	}
}

func TestSessionPath(t *testing.T) {
	home := t.TempDir()
	ps, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	for prof, want := range map[string]string{
		"copilot":        "~/.copilot/session-state/{id}",
		"claude-copilot": "~/.copilot/session-state/{id}",
		"claude":         "~/.claude/projects/*/{id}.jsonl",
		"agy":            "",
		"fake":           "",
	} {
		if got := ps[prof].SessionPath; got != want {
			t.Errorf("%s session_path %q want %q", prof, got, want)
		}
	}
	if got, _ := ps["copilot"].Argv(Vars{ID: "S1"}, Launch{Resume: true}); !reflect.DeepEqual(got, []string{"copilot", "--resume", "S1"}) {
		t.Errorf("resume argv %v", got)
	}

	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "proj-a"), 0o755)
	os.WriteFile(filepath.Join(data, "proj-a", "S1.jsonl"), nil, 0o644)
	os.WriteFile(Path(home), []byte("[claude]\nsession_path = \""+data+"/*/{id}.jsonl\"\n"), 0o644)
	ps, _ = Load(home)
	p := ps["claude"]
	if m := p.SessionMatches("S1"); len(m) != 1 || m[0] != filepath.Join(data, "proj-a", "S1.jsonl") {
		t.Errorf("matches %v", m)
	}
	if m := p.SessionMatches("S2"); len(m) != 0 {
		t.Errorf("unexpected %v", m)
	}
	if m := ps["fake"].SessionMatches("S1"); m != nil {
		t.Errorf("fake %v", m)
	}

	t.Setenv("HOME", data)
	p.SessionPath = "~/proj-a/{id}.jsonl"
	if g := p.SessionGlob("S1"); g != filepath.Join(data, "proj-a", "S1.jsonl") {
		t.Errorf("glob %q", g)
	}
	if m := p.SessionMatches("S1"); len(m) != 1 {
		t.Errorf("~ matches %v", m)
	}
}

func TestModeArgsAndPrompt(t *testing.T) {
	ps, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cop := ps["copilot"]
	got, embedded := cop.Argv(Vars{ID: "U"}, Launch{Mode: "autopilot", Prompt: "do it"})
	want := []string{"copilot", "--allow-all", "--autopilot", "--session-id", "U", "-i", "do it"}
	if !reflect.DeepEqual(got, want) || !embedded {
		t.Errorf("autopilot argv %v embedded=%v want %v true", got, embedded, want)
	}
	// Unknown mode contributes nothing and is reported by HasMode.
	if cop.HasMode("nope") {
		t.Error("HasMode(nope) should be false")
	}
	if got, _ := cop.Argv(Vars{}, Launch{Mode: "nope"}); !reflect.DeepEqual(got, []string{"copilot"}) {
		t.Errorf("unknown mode: %v", got)
	}
	if !cop.HasMode("") || !cop.HasMode("plan") {
		t.Error("HasMode(\"\")/plan")
	}
	// extra_args land after the id args, before the prompt.
	got, _ = cop.Argv(Vars{ID: "U"}, Launch{Extra: []string{"--reasoning-effort", "high"}, Prompt: "p"})
	want = []string{"copilot", "--session-id", "U", "--reasoning-effort", "high", "-i", "p"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extra args %v want %v", got, want)
	}
	// Claude takes the prompt positionally.
	got, embedded = ps["claude"].Argv(Vars{}, Launch{Mode: "accept-edits", Prompt: "hi"})
	want = []string{"claude", "--model", "opus", "--permission-mode", "acceptEdits", "hi"}
	if !reflect.DeepEqual(got, want) || !embedded {
		t.Errorf("claude argv %v embedded=%v want %v true", got, embedded, want)
	}
	// fake deliberately has no prompt_args: it stays on the paste path.
	got, embedded = ps["fake"].Argv(Vars{Fleet: "/bin/fleet", Name: "a1"}, Launch{Prompt: "hi"})
	if embedded || !reflect.DeepEqual(got, []string{"/bin/fleet", "fakeagent", "a1"}) {
		t.Errorf("fake argv %v embedded=%v", got, embedded)
	}
}

// Every built-in profile answers to the one canonical "auto" mode, so a single
// mode = "auto" in the local preferences makes the whole fleet unattended
// (ADR-56). A profile missing it would silently keep prompting.
func TestEveryProfileHasAutoMode(t *testing.T) {
	ps, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"copilot":        {"--allow-all", "--autopilot"},
		"claude-copilot": {"--allow-all", "--autopilot"},
		"claude":         {"--permission-mode", "bypassPermissions"},
		"agy":            {"--dangerously-skip-permissions"},
		"vibe":           {"--auto-approve"},
		"fake":           nil, // the fake agent never asks for permission
	}
	if len(ps) != len(want) {
		t.Errorf("built-in profiles %v; a new one needs an auto mode too", Names(ps))
	}
	for name, flags := range want {
		p, ok := ps[name]
		if !ok {
			t.Errorf("no profile %q", name)
			continue
		}
		if !p.HasMode(AutoMode) {
			t.Errorf("%s: no %q mode", name, AutoMode)
			continue
		}
		got, _ := p.Argv(Vars{}, Launch{Mode: AutoMode})
		if !containsAll(got, flags) {
			t.Errorf("%s auto argv %v, want it to carry %v", name, got, flags)
		}
	}
}

// agy and vibe gained mode_args with ADR-56, so their other modes are worth
// pinning too: a typo in one of these flags is silent at spawn.
func TestAgyAndVibeModes(t *testing.T) {
	ps, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		profile, mode string
		want          []string
	}{
		{"agy", "accept-edits", []string{"agy", "--mode", "accept-edits"}},
		{"agy", "plan", []string{"agy", "--mode", "plan"}},
		{"vibe", "smart-approve", []string{"vibe", "--smart-approve"}},
		{"vibe", "ask", []string{"vibe", "--agent", "ask"}},
	} {
		got, _ := ps[c.profile].Argv(Vars{}, Launch{Mode: c.mode})
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %s argv %v want %v", c.profile, c.mode, got, c.want)
		}
	}
}

func containsAll(argv, want []string) bool {
	have := map[string]bool{}
	for _, a := range argv {
		have[a] = true
	}
	for _, w := range want {
		if !have[w] {
			return false
		}
	}
	return true
}

func TestLocalProfileLayer(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte("[copilot]\nargs = [\"--from-tools\"]\n"), 0o644)
	local, err := conf.Parse(`
[copilot]
args = ["--from-config"]
ready = "ready>"
mode_keys = { autopilot = "C-a p" }

[my-copilot]
command = "copilot"
args = ["--model", "claude-opus-5"]
patterns = "copilot"
`)
	if err != nil {
		t.Fatal(err)
	}
	ps, err := LoadWith(home, local)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := ps["copilot"].Argv(Vars{}, Launch{}); !reflect.DeepEqual(got, []string{"copilot", "--from-config"}) {
		t.Errorf("local layer did not win: %v", got)
	}
	if ps["copilot"].Ready != "ready>" {
		t.Errorf("ready %q", ps["copilot"].Ready)
	}
	if got := ps["copilot"].ModeKeys["autopilot"]; !reflect.DeepEqual(got, []string{"C-a", "p"}) {
		t.Errorf("mode_keys %v", got)
	}
	// Built-in fields not mentioned locally survive.
	if !reflect.DeepEqual(ps["copilot"].PromptArgs, []string{"-i", "{prompt}"}) {
		t.Errorf("prompt_args lost: %v", ps["copilot"].PromptArgs)
	}
	m := ps["my-copilot"]
	if m.Command != "copilot" || m.Patterns != "copilot" {
		t.Errorf("new profile %+v", m)
	}
}

// auto_submit = false must fall back to the paste path: `copilot -i <prompt>`
// submits the prompt itself, so embedding it would ignore the preference.
func TestArgvNoPromptArgs(t *testing.T) {
	p := Profile{Command: "copilot", PromptArgs: []string{"-i", "{prompt}"}}
	argv, embedded := p.Argv(Vars{}, Launch{Prompt: "hi", NoPromptArgs: true})
	if embedded {
		t.Error("prompt embedded despite NoPromptArgs")
	}
	if strings.Join(argv, " ") != "copilot" {
		t.Errorf("argv %v", argv)
	}
	if _, embedded := p.Argv(Vars{}, Launch{Prompt: "hi"}); !embedded {
		t.Error("prompt should embed by default")
	}
}

// A typo'd [profile.<name>] in the local config must not materialise a profile
// with an empty command that `fleet profiles` lists and that spawns a corpse.
func TestLoadWithIgnoresCommandlessLocalSections(t *testing.T) {
	home := t.TempDir()
	local := conf.Doc{
		"coplot":  {"mode": "autopilot"},                     // typo: no command, no such profile
		"copilot": {"args": []string{"--model", "x"}},        // overrides a built-in
		"mine":    {"command": "mytool", "patterns": "fake"}, // defines a new profile
	}
	ps, err := LoadWith(home, local)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ps["coplot"]; ok {
		t.Error("command-less local section created a profile")
	}
	if got := ps["copilot"].Args; !reflect.DeepEqual(got, []string{"--model", "x"}) {
		t.Errorf("copilot args %v", got)
	}
	if ps["mine"].Command != "mytool" {
		t.Errorf("mine = %+v", ps["mine"])
	}
}

func TestPeerArgs(t *testing.T) {
	ps, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for prof, flag := range map[string]string{"copilot": "--additional-mcp-config", "claude-copilot": "--additional-mcp-config",
		"claude": "--mcp-config"} {
		argv, _ := ps[prof].Argv(AgentVars("/bin/fleet", "a1", "U"), Launch{Resume: true})
		if len(argv) < 3 || argv[1] != flag {
			t.Fatalf("%s: peer args not right after command: %v", prof, argv)
		}
		var cfg struct {
			MCPServers map[string]struct {
				Type    string   `json:"type"`
				Command string   `json:"command"`
				Args    []string `json:"args"`
				Tools   []string `json:"tools"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(argv[2]), &cfg); err != nil {
			t.Fatalf("%s: bad json %q: %v", prof, argv[2], err)
		}
		s := cfg.MCPServers["fleet-peer"]
		if s.Command != "/bin/fleet" || !reflect.DeepEqual(s.Args, []string{"mcp", "--agent", "a1"}) {
			t.Errorf("%s: server %+v", prof, s)
		}
		wantType, wantTools := "stdio", 0
		if flag == "--additional-mcp-config" {
			wantType, wantTools = "local", 1
		}
		if s.Type != wantType || len(s.Tools) != wantTools {
			t.Errorf("%s: type %q tools %v", prof, s.Type, s.Tools)
		}
	}
	for _, prof := range []string{"agy", "vibe", "fake"} {
		if argv, _ := ps[prof].Argv(AgentVars("/bin/fleet", "a1", ""), Launch{}); strings.Contains(strings.Join(argv, " "), "mcp") {
			t.Errorf("%s has peer args: %v", prof, argv)
		}
	}
}

func TestSubagentLogPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ps, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".copilot", "session-state", "S1", "events.jsonl")
	for _, n := range []string{"copilot", "claude-copilot"} {
		if g := ps[n].SubagentLogPath("S1"); g != want {
			t.Errorf("%s: %q, want %q", n, g, want)
		}
		if g := ps[n].SubagentLogPath(""); g != "" {
			t.Errorf("%s: empty id gave %q", n, g)
		}
	}
	for _, n := range []string{"claude", "agy", "vibe", "fake"} {
		if g := ps[n].SubagentLogPath("S1"); g != "" {
			t.Errorf("%s has a subagent log: %q", n, g)
		}
	}
	os.WriteFile(Path(home), []byte("[fake]\nsubagent_log = \"/tmp/{id}.log\"\n"), 0o644)
	ps, _ = Load(home)
	if g := ps["fake"].SubagentLogPath("X"); g != "/tmp/X.log" {
		t.Errorf("override: %q", g)
	}
}

// Claude gets status hooks through its own --settings argument, so fleet
// never edits the user's ~/.claude/settings.json (ADR-49).
func TestClaudeHookArgs(t *testing.T) {
	ps, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	argv, _ := ps["claude"].Argv(AgentVars("/bin/fleet", "a1", ""), Launch{})
	i := indexOf(argv, "--settings")
	if i < 0 || i+1 >= len(argv) {
		t.Fatalf("claude argv has no --settings: %v", argv)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(argv[i+1]), &doc); err != nil {
		t.Fatalf("bad settings json %q: %v", argv[i+1], err)
	}
	for _, e := range []string{"Stop", "Notification", "PreToolUse"} {
		h, ok := doc.Hooks[e]
		if !ok || len(h) == 0 || len(h[0].Hooks) == 0 {
			t.Fatalf("no %s hook: %v", e, doc.Hooks)
		}
		cmd := h[0].Hooks[0]
		if cmd.Type != "command" || !strings.Contains(cmd.Command, "/bin/fleet") ||
			!strings.Contains(cmd.Command, "hook "+strings.ToLower(e)) ||
			!strings.Contains(cmd.Command, "--agent") {
			t.Errorf("%s command %q", e, cmd.Command)
		}
	}
	// Tools without hook_args are launched unchanged.
	for _, prof := range []string{"copilot", "agy", "vibe", "fake"} {
		if argv, _ := ps[prof].Argv(AgentVars("/bin/fleet", "a1", ""), Launch{}); indexOf(argv, "--settings") >= 0 {
			t.Errorf("%s got hook args: %v", prof, argv)
		}
	}
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}

// TestClaudeModelsAreNewest fails when a built-in or example profile starts a
// Claude model other than the newest: the claude CLI must use an alias that
// tracks the newest model, Copilot must use CopilotClaudeModel (ADR-58).
func TestClaudeModelsAreNewest(t *testing.T) {
	builtin, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../../examples/tools.toml")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tools.toml"), b, 0o644)
	example, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for src, ps := range map[string]map[string]Profile{"built-in": builtin, "example": example} {
		for name, p := range ps {
			model := ""
			for i, a := range p.Args {
				if a == "--model" && i+1 < len(p.Args) {
					model = p.Args[i+1]
				}
			}
			switch {
			case p.Command == "claude" && model != ClaudeModel:
				t.Errorf("%s %s: --model %q, want the alias %q", src, name, model, ClaudeModel)
			case p.Command == "claude" && p.Env["CLAUDE_CODE_SUBAGENT_MODEL"] != ClaudeSubagentModel:
				t.Errorf("%s %s: subagent model %q, want %q", src, name, p.Env["CLAUDE_CODE_SUBAGENT_MODEL"], ClaudeSubagentModel)
			case p.Command == "copilot" && strings.HasPrefix(model, "claude") && model != CopilotClaudeModel:
				t.Errorf("%s %s: --model %q, want %q", src, name, model, CopilotClaudeModel)
			}
		}
	}
}

// The Copilot profiles start lean by default: built-in tools no fleet agent
// uses are excluded at every level, and the subagent tools only for light
// and standard (ADR-60). A tools.toml context_args replaces the table.
func TestContextArgs(t *testing.T) {
	ps, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, prof := range []string{"copilot", "claude-copilot"} {
		for lvl, agents := range map[string]bool{"light": false, "standard": false, "pro": true, "leader": true} {
			got, _ := ps[prof].Argv(Vars{ID: "U"}, Launch{Context: lvl})
			i := slices.Index(got, "--excluded-tools")
			if i < 0 || !slices.Contains(got, "sql") || !slices.Contains(got, "--disable-builtin-mcps") {
				t.Fatalf("%s %s: %v", prof, lvl, got)
			}
			if slices.Contains(got, "task") != !agents {
				t.Errorf("%s %s: task excluded=%v, want %v: %v", prof, lvl, slices.Contains(got, "task"), !agents, got)
			}
			// --excluded-tools is variadic: everything up to the next flag is a
			// tool name, so nothing fleet needs may land in that run.
			for _, a := range got[i+1:] {
				if strings.HasPrefix(a, "-") {
					break
				}
				if slices.Contains([]string{"bash", "view", "edit", "create", "grep", "glob", "skill", "web_fetch", "ask_user", "task_complete", "tool_search_tool"}, a) {
					t.Errorf("%s %s excludes %q", prof, lvl, a)
				}
			}
		}
	}
	if got, _ := ps["claude"].Argv(Vars{}, Launch{Context: "light"}); slices.Contains(got, "--excluded-tools") {
		t.Errorf("claude got copilot context args: %v", got)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tools.toml"), []byte("[copilot]\ncontext_args = { light = \"--x\" }\n"), 0o644)
	ps, _ = Load(dir)
	if got, _ := ps["copilot"].Argv(Vars{}, Launch{Context: "light"}); !reflect.DeepEqual(got, []string{"copilot", "--x"}) {
		t.Errorf("override light: %v", got)
	}
	if got, _ := ps["copilot"].Argv(Vars{}, Launch{Context: "pro"}); !reflect.DeepEqual(got, []string{"copilot"}) {
		t.Errorf("override drops the other levels: %v", got)
	}
}
