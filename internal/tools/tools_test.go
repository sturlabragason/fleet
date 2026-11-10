package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
		{"claude", false, "U", []string{"claude", "--model", "opus", "--exclude-dynamic-system-prompt-sections", "--session-id", "U"}, "claude"},
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
	want = []string{"claude", "--model", "opus", "--exclude-dynamic-system-prompt-sections", "--permission-mode", "acceptEdits", "hi"}
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
	if strings.Contains(argv[i+1], "permissions") {
		t.Fatalf("unfenced settings carry permissions: %s", argv[i+1])
	}
	// In a fleet worktree the file tools are fenced to <repo>.wt/ (ADR-79).
	argv, _ = ps["claude"].Argv(AgentVars("/bin/fleet", "a1", "").Fenced("/src/repo.wt"), Launch{})
	var fenced struct {
		Hooks       map[string]any `json:"hooks"`
		Permissions struct {
			Block bool     `json:"blockWritesOutsideWorkingDirectories"`
			Dirs  []string `json:"additionalDirectories"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(argv[indexOf(argv, "--settings")+1]), &fenced); err != nil {
		t.Fatal(err)
	}
	if !fenced.Permissions.Block || len(fenced.Permissions.Dirs) != 1 || fenced.Permissions.Dirs[0] != "/src/repo.wt" || len(fenced.Hooks) == 0 {
		t.Fatalf("fenced settings %+v", fenced)
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
// tracks the newest model, Copilot must use CopilotClaudeModel (ADR-63).
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

func TestModelArg(t *testing.T) {
	for _, c := range []struct {
		lists [][]string
		want  string
	}{
		{nil, ""},
		{[][]string{{"--allow-all"}}, ""},
		{[][]string{{"--model", "a", "--allow-all"}}, "a"},
		{[][]string{{"--model", "a"}, {"--model=b"}}, "b"}, // extra_args win
		{[][]string{{"--model"}}, ""},
	} {
		if got := ModelArg(c.lists...); got != c.want {
			t.Errorf("%v: %q, want %q", c.lists, got, c.want)
		}
	}
}

func TestACPArgv(t *testing.T) {
	ps, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	v := Vars{Fleet: "/bin/fleet", Name: "a1", ID: "U"}
	cases := []struct {
		profile, mode string
		want          []string
	}{
		{"copilot", "", []string{"copilot", "--acp"}},
		{"copilot", AutoMode, []string{"copilot", "--acp", "--allow-all"}},
		{"claude-copilot", AutoMode, []string{"copilot", "--model", CopilotClaudeModel, "--allow-all", "--acp", "--allow-all"}},
		{"agy", "", []string{"agy", "acp"}},
		{"agy", AutoMode, []string{"agy", "acp", "--dangerously-skip-permissions"}},
		{"fake", AutoMode, []string{"/bin/fleet", "fakeagent", "a1", "--acp"}},
	}
	for _, c := range cases {
		p := ps[c.profile]
		if !p.HasACP() || !p.HasACPMode(c.mode) {
			t.Errorf("%s: HasACP %v HasACPMode(%q) %v", c.profile, p.HasACP(), c.mode, p.HasACPMode(c.mode))
		}
		if got := p.ACPArgv(v, Launch{Mode: c.mode, Prompt: "hi", Resume: true}); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s mode %q: %q want %q", c.profile, c.mode, got, c.want)
		}
	}
	for _, n := range []string{"vibe"} {
		if ps[n].HasACP() {
			t.Errorf("%s: has no ACP server, got %v", n, ps[n].ACP)
		}
	}
	// Peer MCP args go in, the context args come last before extra args.
	cp, _ := PeerMCP("/bin/fleet", "a1")
	got := ps["copilot"].ACPArgv(Vars{Fleet: "/bin/fleet", Name: "a1", PeerMCP: cp}, Launch{Context: "pro", Extra: []string{"--x"}})
	if got[1] != "--additional-mcp-config" || got[3] != "--acp" || got[len(got)-2] != "--disable-builtin-mcps" || got[len(got)-1] != "--x" {
		t.Errorf("copilot acp argv with peer/context/extra: %q", got)
	}
}

func TestACPUserOverride(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte("[vibe]\nacp = [\"--acp\"]\nacp_mode_args = { auto = \"--yolo --x\" }\n"), 0o644)
	ps, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	got := ps["vibe"].ACPArgv(Vars{}, Launch{Mode: AutoMode})
	if want := []string{"vibe", "--acp", "--yolo", "--x"}; !reflect.DeepEqual(got, want) {
		t.Errorf("%q want %q", got, want)
	}
}

// Claude over the adapter (ADR-76): acp_command replaces the claude argv, the
// mode is a session mode, the model and the claude binary go in the env, and
// peer MCP goes over the session, not argv.
func TestClaudeACPProfile(t *testing.T) {
	ps, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := ps["claude"]
	if !p.HasACP() || p.Driver != "" {
		t.Fatalf("claude: HasACP %v driver %q; want an ACP server and no default driver (opt-in)", p.HasACP(), p.Driver)
	}
	if !p.ACPOptIn() {
		t.Error("claude: an adapter profile must be opt-in")
	}
	for _, n := range []string{"copilot", "agy", "fake"} {
		if ps[n].ACPOptIn() {
			t.Errorf("%s: native ACP is not opt-in", n)
		}
	}
	cp, cl := PeerMCP("/bin/fleet", "a1")
	v := Vars{Fleet: "/bin/fleet", Name: "a1", PeerMCP: cp, PeerMCPClaude: cl, Hooks: "{}"}
	want := []string{"npx", "-y", "@agentclientprotocol/claude-agent-acp@" + ClaudeACPVersion}
	for _, mode := range []string{"", AutoMode, "plan"} {
		got := p.ACPArgv(v, Launch{Mode: mode, Context: "pro", Extra: []string{"--model", "x"}, Prompt: "hi"})
		if !reflect.DeepEqual(got, want) {
			t.Errorf("mode %q: %q want %q", mode, got, want)
		}
	}
	for mode, id := range map[string]string{"": "default", AutoMode: "bypassPermissions", "bypass": "bypassPermissions", "accept-edits": "acceptEdits", "plan": "plan"} {
		if !p.HasACPMode(mode) || p.ACPSessionMode(mode) != id {
			t.Errorf("mode %q: HasACPMode %v session mode %q want %q", mode, p.HasACPMode(mode), p.ACPSessionMode(mode), id)
		}
	}
	if p.HasACPMode("bogus") || p.ACPSessionMode("bogus") != "" {
		t.Error("unknown mode accepted")
	}
	if !p.ACPSessionMCP() || ps["copilot"].ACPSessionMCP() {
		t.Error("peer MCP: session for claude, argv for copilot")
	}
	env := p.ACPEnv("/usr/local/bin/claude")
	if env["CLAUDE_CODE_EXECUTABLE"] != "/usr/local/bin/claude" || env["ANTHROPIC_MODEL"] != ClaudeModel ||
		env["CLAUDE_CODE_SUBAGENT_MODEL"] != ClaudeSubagentModel {
		t.Errorf("acp env %v", env)
	}
	// Terms (ADR-76): no API key or token is ever set, and one inherited is dropped.
	for _, k := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if _, ok := env[k]; ok {
			t.Errorf("claude acp env sets %s", k)
		}
		if !slices.Contains(p.ACPUnsetEnv, k) {
			t.Errorf("claude acp does not drop an inherited %s: %v", k, p.ACPUnsetEnv)
		}
	}
	if !p.ACPNeedsCommandPath() || ps["copilot"].ACPNeedsCommandPath() {
		t.Error("{command_path} only in claude's acp_env")
	}
	// Copilot's ACP env is its env.
	if e := ps["copilot"].ACPEnv("x"); len(e) != 0 {
		t.Errorf("copilot acp env %v", e)
	}
}

// The tmux argv of claude is what it was before ADR-76, plus the
// cache-friendly system prompt flag (PF7).
func TestClaudeTmuxArgvUnchanged(t *testing.T) {
	ps, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cp, cl := PeerMCP("/bin/fleet", "a1")
	got, embedded := ps["claude"].Argv(Vars{Fleet: "/bin/fleet", Name: "a1", ID: "U", PeerMCP: cp, PeerMCPClaude: cl, Hooks: "H"},
		Launch{Mode: AutoMode, Prompt: "hi"})
	want := []string{"claude", "--mcp-config", cl, "--settings", "H", "--model", ClaudeModel,
		"--exclude-dynamic-system-prompt-sections", "--permission-mode", "bypassPermissions", "--session-id", "U", "hi"}
	if !embedded || !reflect.DeepEqual(got, want) {
		t.Errorf("%q\nwant %q", got, want)
	}
	if e := ps["claude"].Env; len(e) != 1 || e["CLAUDE_CODE_SUBAGENT_MODEL"] != ClaudeSubagentModel {
		t.Errorf("claude tmux env %v", e)
	}
}

// acp_from copies the acp keys a profile doesn't set from another profile,
// so per-account claude profiles share the built-in's pinned adapter.
func TestACPFrom(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[claude-work]
command = "claude"
env = { CLAUDE_CONFIG_DIR = "/x/.claude-work" }
patterns = "claude"
acp_from = "claude"

[claude-own]
command = "claude"
acp_from = "claude"
acp_env = { ANTHROPIC_MODEL = "haiku", CLAUDE_CODE_EXECUTABLE = "{command_path}" }

[bad]
command = "x"
acp_from = "nope"
`), 0o644)
	ps, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	w, c := ps["claude-work"], ps["claude"]
	if !reflect.DeepEqual(w.ACPCommand, c.ACPCommand) || !reflect.DeepEqual(w.ACPModes, c.ACPModes) ||
		!w.ACPSessionMCP() || !reflect.DeepEqual(w.ACPUnsetEnv, c.ACPUnsetEnv) || w.Driver != "" {
		t.Errorf("claude-work did not take claude's acp keys: %+v", w)
	}
	if e := w.ACPEnv("/b/claude"); e["CLAUDE_CONFIG_DIR"] != "/x/.claude-work" || e["CLAUDE_CODE_EXECUTABLE"] != "/b/claude" {
		t.Errorf("claude-work acp env %v", e)
	}
	if e := ps["claude-own"].ACPEnv("/b/claude"); e["ANTHROPIC_MODEL"] != "haiku" {
		t.Errorf("own acp_env lost: %v", e)
	}
	if ps["bad"].HasACP() {
		t.Error("acp_from an unknown profile gave an ACP server")
	}
}

func TestACPLabel(t *testing.T) {
	ps, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for prof, want := range map[string]string{
		"copilot": "copilot --acp",
		"agy":     "agy acp",
		"claude":  "claude-agent-acp@" + ClaudeACPVersion,
		"vibe":    "",
	} {
		if got := ps[prof].ACPLabel(); got != want {
			t.Errorf("%s: ACPLabel %q, want %q", prof, got, want)
		}
	}
}

// ADR-74 (i): the acp default follows the tool, not the profile name. Every
// profile that runs copilot or agy defaults to acp, including claude-copilot
// and one the user defines without acp keys; an explicit driver wins; the
// claude adapter stays opt-in; tools without native ACP stay on tmux.
func TestDefaultDriverFollowsTheTool(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[claude-copilot]
command = "copilot"
args = ["--model", "claude-opus-5.5"]

[gpt-copilot]
command = "/opt/bin/copilot"
args = ["--model", "gpt-x"]

[copilot-tui]
command = "copilot"
driver = "tmux"

[agy-work]
command = "agy"

[claude-work]
command = "claude"
acp_from = "claude"

[myvibe]
command = "vibe"
`), 0o644)
	ps, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"copilot": "acp", "claude-copilot": "acp", "gpt-copilot": "acp", "agy": "acp", "agy-work": "acp",
		"copilot-tui": "tmux", "claude": "", "claude-work": "", "vibe": "", "myvibe": "", "fake": "",
	} {
		p := ps[name]
		if got := p.DefaultDriver(); got != want {
			t.Errorf("%s: DefaultDriver %q want %q", name, got, want)
		}
		if want == "acp" && !p.HasACP() {
			t.Errorf("%s: defaults to acp but has no ACP server", name)
		}
	}
	// The built-in claude-copilot, with no user file at all.
	if ps, _ := Load(t.TempDir()); ps["claude-copilot"].DefaultDriver() != "acp" {
		t.Errorf("built-in claude-copilot: DefaultDriver %q want acp", ps["claude-copilot"].DefaultDriver())
	}
}

// claude-copilot under acp keeps its model and gets the peer MCP server on
// argv, with the user's override of args (no --allow-all) and the built-in.
func TestClaudeCopilotACPArgv(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte("[my-cc]\ncommand = \"copilot\"\nargs = [\"--model\", \"claude-opus-5.5\"]\npeer_args = [\"--additional-mcp-config\", \"{peer_mcp}\"]\n"), 0o644)
	ps, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	cp, _ := PeerMCP("/bin/fleet", "a1")
	v := Vars{Fleet: "/bin/fleet", Name: "a1", PeerMCP: cp}
	for _, c := range []struct {
		profile, mode string
		want          []string
	}{
		{"claude-copilot", "autopilot", []string{"copilot", "--additional-mcp-config", cp, "--model", CopilotClaudeModel, "--allow-all", "--acp", "--allow-all"}},
		{"my-cc", "autopilot", []string{"copilot", "--additional-mcp-config", cp, "--model", "claude-opus-5.5", "--acp", "--allow-all"}},
		{"my-cc", "", []string{"copilot", "--additional-mcp-config", cp, "--model", "claude-opus-5.5", "--acp"}},
	} {
		p := ps[c.profile]
		if !p.HasACPMode(c.mode) {
			t.Errorf("%s: no acp mode %q", c.profile, c.mode)
		}
		if got := p.ACPArgv(v, Launch{Mode: c.mode}); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s mode %q:\n%q\nwant %q", c.profile, c.mode, got, c.want)
		}
	}
}

// A user profile that runs claude or copilot and sets no peer_args or
// hook_args takes the built-in's: without them its agents have no
// fleet_report tool and no status hooks. An explicit [] still opts out.
func TestUserProfileInheritsPeerAndHookArgs(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(Path(dir), []byte(`[claude-personal]
command = "claude"
env = { CLAUDE_CONFIG_DIR = "/x" }
[copilot-work]
command = "copilot"
[claude-bare]
command = "/usr/local/bin/claude"
peer_args = []
hook_args = []
[other]
command = "foo"
`), 0o644)
	ps, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ps["claude-personal"].PeerArgs, ps["claude"].PeerArgs) || !reflect.DeepEqual(ps["claude-personal"].HookArgs, ps["claude"].HookArgs) {
		t.Errorf("claude-personal: peer %v hook %v", ps["claude-personal"].PeerArgs, ps["claude-personal"].HookArgs)
	}
	argv, _ := ps["claude-personal"].Argv(AgentVars("/bin/fleet", "a1", ""), Launch{})
	if len(argv) < 2 || argv[1] != "--mcp-config" || indexOf(argv, "--settings") < 0 {
		t.Errorf("claude-personal argv: %v", argv)
	}
	if !reflect.DeepEqual(ps["copilot-work"].PeerArgs, ps["copilot"].PeerArgs) || ps["copilot-work"].HookArgs != nil {
		t.Errorf("copilot-work: peer %v hook %v", ps["copilot-work"].PeerArgs, ps["copilot-work"].HookArgs)
	}
	if len(ps["claude-bare"].PeerArgs) != 0 || len(ps["claude-bare"].HookArgs) != 0 || ps["claude-bare"].UsesPlugin(false) {
		t.Errorf("explicit [] not kept: %+v", ps["claude-bare"])
	}
	// The plugin (ADR-99) comes along only with both: it carries the hooks
	// and, for copilot, fleet-peer.
	if !reflect.DeepEqual(ps["copilot-work"].PluginArgs, ps["copilot"].PluginArgs) || !ps["copilot-work"].PluginMCP || !ps["claude-personal"].UsesPlugin(false) {
		t.Errorf("plugin not inherited: %+v", ps["copilot-work"])
	}
	if ps["other"].PeerArgs != nil || ps["other"].HookArgs != nil {
		t.Errorf("other: %+v", ps["other"])
	}
}

// Worker spawns switch off the user's global MCP servers, keeping the ones
// named in keep (ADR-91): copilot on both drivers, claude by denying the
// server's tools.
func TestUserMCPOff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := `{"mcpServers":{"transformradar":{"command":"tr"},"kept":{"command":"k"},"fleet-peer":{"command":"x"}}}`
	os.MkdirAll(filepath.Join(home, ".copilot"), 0o755)
	os.WriteFile(filepath.Join(home, ".copilot", "mcp-config.json"), []byte(cfg), 0o644)
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(cfg), 0o644)
	ps, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	keep := []string{"kept"}
	for _, prof := range []string{"copilot", "claude-copilot"} {
		p := ps[prof]
		v := AgentVars("/bin/fleet", "a1", "U").UserMCP(p, keep)
		tm, _ := p.Argv(v, Launch{})
		for name, argv := range map[string][]string{"tmux": tm, "acp": p.ACPArgv(v, Launch{})} {
			if !containsSeq(argv, "--disable-mcp-server", "transformradar") || slices.Contains(argv, "kept") ||
				strings.Count(strings.Join(argv, " "), "--disable-mcp-server") != 1 {
				t.Errorf("%s %s: %q", prof, name, argv)
			}
		}
	}
	v := AgentVars("/bin/fleet", "a1", "U").UserMCP(ps["claude"], keep)
	argv, _ := ps["claude"].Argv(v, Launch{})
	if !slices.Contains(argv, "--disallowedTools=mcp__transformradar") || strings.Contains(strings.Join(argv, " "), "mcp__kept") {
		t.Errorf("claude: %q", argv)
	}
	// An arg list without {server} is added once.
	p := Profile{Command: "x", UserMCPOff: []string{"--strict"}, UserMCPConfig: "~/.claude.json"}
	if argv, _ := p.Argv(AgentVars("/bin/fleet", "a1", "").UserMCP(p, nil), Launch{}); !reflect.DeepEqual(argv, []string{"x", "--strict"}) {
		t.Errorf("once: %q", argv)
	}
	// No user servers: nothing added.
	os.Remove(filepath.Join(home, ".copilot", "mcp-config.json"))
	v = AgentVars("/bin/fleet", "a1", "U").UserMCP(ps["copilot"], nil)
	if argv, _ := ps["copilot"].Argv(v, Launch{}); slices.Contains(argv, "--disable-mcp-server") {
		t.Errorf("no config: %q", argv)
	}
	// user_mcp_off = [] in a profile turns it off.
	dir := t.TempDir()
	os.WriteFile(Path(dir), []byte("[claude-all]\ncommand = \"claude\"\nuser_mcp_off = []\n"), 0o644)
	ps, _ = Load(dir)
	v = AgentVars("/bin/fleet", "a1", "U").UserMCP(ps["claude-all"], nil)
	if argv, _ := ps["claude-all"].Argv(v, Launch{}); strings.Contains(strings.Join(argv, " "), "disallowed") {
		t.Errorf("user_mcp_off = [] still strips: %q", argv)
	}
}

func containsSeq(s []string, a, b string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == a && s[i+1] == b {
			return true
		}
	}
	return false
}

// ADR-100: WithModel swaps the profile's own model for the picked one on
// the command line and in an ACP adapter's model variable; a tool with no
// model flag is left alone.
func TestWithModel(t *testing.T) {
	ps, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := ps["claude"].WithModel("claude-opus-4-8")
	if got := ModelArg(c.Args); got != "claude-opus-4-8" || indexOf(c.Args, ClaudeModel) >= 0 {
		t.Errorf("claude args %v", c.Args)
	}
	if c.ACPEnvVars["ANTHROPIC_MODEL"] != "claude-opus-4-8" || ps["claude"].ACPEnvVars["ANTHROPIC_MODEL"] != ClaudeModel {
		t.Errorf("acp env %v, original %v", c.ACPEnvVars, ps["claude"].ACPEnvVars)
	}
	cc := ps["copilot"].WithModel(CopilotClaudeModel)
	if got := ModelArg(cc.Args); got != CopilotClaudeModel {
		t.Errorf("copilot args %v", cc.Args)
	}
	if v := ps["vibe"]; !reflect.DeepEqual(v.WithModel("x").Args, v.Args) || v.HasModelFlag() {
		t.Errorf("vibe changed: %v", v.WithModel("x").Args)
	}
	if got := ps["claude"].StripModel([]string{"--model=a", "-x", "--model", "b", "y"}); !reflect.DeepEqual(got, []string{"-x", "y"}) {
		t.Errorf("StripModel %v", got)
	}
}

// Every built-in or inherited profile with a model flag pins Opus, so no
// fleet agent starts on a tool's own default model (ADR-100).
func TestModelFlagProfilesPinOpus(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(Path(dir), []byte("[cp-work]\ncommand = \"copilot\"\n[cl-work]\ncommand = \"claude\"\n"), 0o644)
	ps, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"copilot", "claude-copilot", "claude", "cp-work", "cl-work"} {
		p := ps[name]
		if !p.HasModelFlag() || !IsOpus(p.DefaultModel) {
			t.Errorf("%s: model_args %v default_model %q", name, p.ModelArgs, p.DefaultModel)
		}
		if m := ModelArg(p.Args); m != "" && !IsOpus(m) {
			t.Errorf("%s: args name %q", name, m)
		}
	}
	if !IsOpus("claude-opus-5.5") || IsOpus("claude-sonnet-5.5") || IsOpus("haiku") {
		t.Error("IsOpus")
	}
}

// Two agents of one profile get the same argv apart from their own name,
// id, peer config, hooks and plugin dir, in one order shared by both
// drivers, so they share the tool's cached prompt prefix (PF7).
func TestArgvIdenticalAcrossAgents(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "mcp.json")
	os.WriteFile(cfg, []byte(`{"mcpServers":{"zeta":{},"alpha":{},"mid":{},"fleet":{}}}`), 0o644)
	ps, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"copilot", "claude-copilot", "claude"} {
		p := ps[name]
		p.UserMCPConfig = cfg
		build := func(agent, id string) (tm, acp []string, v Vars) {
			v = AgentVars("/bin/fleet", agent, id).UserMCP(p, nil)
			if len(p.PluginArgs) > 0 {
				v = v.WithPlugin("/h/.fleet/plugins/"+agent, p)
			}
			tm, _ = p.Argv(v, Launch{Mode: AutoMode, Context: "standard"})
			return tm, p.ACPArgv(v, Launch{Mode: AutoMode, Context: "standard"}), v
		}
		mask := func(argv []string, v Vars) string {
			r := strings.NewReplacer(v.Plugin, "<plugin>", v.Hooks, "<hooks>", v.PeerMCP, "<peer>",
				v.PeerMCPClaude, "<peer>", v.ID, "<id>")
			return r.Replace(strings.Join(argv, "\x00"))
		}
		t1, a1, v1 := build("a1", "11111111-1111")
		t2, a2, v2 := build("a2", "22222222-2222")
		if m1, m2 := mask(t1, v1), mask(t2, v2); m1 != m2 {
			t.Errorf("%s tmux argv differs:\n%q\n%q", name, m1, m2)
		}
		if m1, m2 := mask(a1, v1), mask(a2, v2); m1 != m2 {
			t.Errorf("%s acp argv differs:\n%q\n%q", name, m1, m2)
		}
		for _, argv := range [][]string{t1, a1} {
			if argv[0] == "npx" {
				continue // an adapter takes none of the tool's flags (ADR-76)
			}
			j := strings.Join(argv, " ")
			if !(strings.Index(j, "alpha") < strings.Index(j, "mid") && strings.Index(j, "mid") < strings.Index(j, "zeta")) {
				t.Errorf("%s: MCP servers not sorted: %q", name, argv)
			}
			// peer and plugin come before the user MCP switches, then hooks.
			last := -1
			for _, f := range []string{"--additional-mcp-config", "--mcp-config", "--plugin-dir", "alpha", "--settings"} {
				k := strings.Index(j, f)
				if k < 0 {
					continue
				}
				if k < last {
					t.Errorf("%s: %s out of order in %q", name, f, argv)
				}
				last = k
			}
		}
	}
}

// native_binary starts the binary the Node launcher would run, and keeps
// the launcher when there is none (PF6).
func TestNativeBinary(t *testing.T) {
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	if arch == "" || (runtime.GOOS != "linux" && runtime.GOOS != "darwin") {
		t.Skip("no native Copilot package for this platform")
	}
	root := t.TempDir()
	pkg := filepath.Join(root, "lib", "node_modules", "@github", "copilot")
	native := filepath.Join(root, "lib", "node_modules", "@github", "copilot-"+runtime.GOOS+"-"+arch, "copilot")
	os.MkdirAll(pkg, 0o755)
	os.MkdirAll(filepath.Dir(native), 0o755)
	os.WriteFile(filepath.Join(pkg, "npm-loader.js"), []byte("#!/usr/bin/env node\n"), 0o755)
	os.WriteFile(native, []byte("\x7fELF"), 0o755)
	os.MkdirAll(filepath.Join(root, "bin"), 0o755)
	launcher := filepath.Join(root, "bin", "copilot")
	os.Symlink(filepath.Join(pkg, "npm-loader.js"), launcher)

	want, _ := filepath.EvalSymlinks(native)
	p := Profile{Command: launcher, NativeBinary: true}
	if got, _ := p.Argv(Vars{}, Launch{}); got[0] != want {
		t.Errorf("tmux argv[0] %q want %q", got[0], want)
	}
	if got := p.ACPArgv(Vars{}, Launch{}); got[0] != want {
		t.Errorf("acp argv[0] %q want %q", got[0], want)
	}
	if got, err := NativeBinary(native); err != nil || got != want {
		t.Errorf("a native command: %q %v", got, err)
	}
	os.Remove(native)
	if got, _ := p.Argv(Vars{}, Launch{}); got[0] != launcher {
		t.Errorf("no native binary: argv[0] %q, want the launcher", got[0])
	}
	if got, _ := (Profile{Command: launcher}).Argv(Vars{}, Launch{}); got[0] != launcher {
		t.Errorf("off by default: %q", got[0])
	}
	ps, err := LoadWith(t.TempDir(), conf.Doc{"copilot": {"native_binary": "true"}})
	if err != nil || !ps["copilot"].NativeBinary || ps["claude-copilot"].NativeBinary {
		t.Errorf("native_binary key: %v %v", ps["copilot"].NativeBinary, err)
	}
}
