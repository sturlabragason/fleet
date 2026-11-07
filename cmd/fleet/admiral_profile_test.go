package main

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/tools"
)

func init() {
	// The test profiles never look "ready": don't wait 15 s to paste the boot prompt.
	bootPasteTimeout = 200 * time.Millisecond
}

func TestResolveAdmiralProfile(t *testing.T) {
	ps := map[string]tools.Profile{"copilot": {}, "claude-personal": {}, "fake": {}}
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	none := env(nil)
	cases := []struct {
		name, explicit    string
		env               map[string]string
		persisted, config string
		want              string
		warns             int
	}{
		{"default", "", nil, "", "", "copilot", 0},
		{"flag wins", "fake", map[string]string{"FLEET_ADMIRAL_PROFILE": "claude-personal"}, "copilot", "copilot", "fake", 0},
		{"admiral env", "", map[string]string{"FLEET_ADMIRAL_PROFILE": "claude-personal", "FLEET_SUPERVISOR_PROFILE": "fake"}, "", "", "claude-personal", 0},
		{"supervisor env", "", map[string]string{"FLEET_SUPERVISOR_PROFILE": "fake"}, "claude-personal", "", "fake", 0},
		{"last choice over config", "", nil, "claude-personal", "fake", "claude-personal", 0},
		{"config", "", nil, "", "fake", "fake", 0},
		{"unknown skipped", "", map[string]string{"FLEET_ADMIRAL_PROFILE": "gone"}, "old", "fake", "fake", 2},
	}
	for _, c := range cases {
		e := none
		if c.env != nil {
			e = env(c.env)
		}
		got, warns, err := resolveAdmiralProfile(c.explicit, ps, e, c.persisted, c.config)
		if err != nil || got != c.want || len(warns) != c.warns {
			t.Errorf("%s: got %q warns %v err %v", c.name, got, warns, err)
		}
	}
	if _, _, err := resolveAdmiralProfile("nope", ps, none, "", ""); err == nil || !strings.Contains(err.Error(), `no profile "nope"`) {
		t.Errorf("unknown flag: %v", err)
	}
}

func TestTopLevelProfile(t *testing.T) {
	for _, c := range []struct {
		in, want []string
		err      bool
	}{
		{[]string{"ls"}, []string{"ls"}, false},
		{[]string{"--admiral-profile", "claude"}, []string{"up", "--admiral-profile", "claude"}, false},
		{[]string{"--admiral-profile=claude", "up", "--no-attach"}, []string{"up", "--admiral-profile", "claude", "--no-attach"}, false},
		{[]string{"--supervisor-profile", "x", "up"}, []string{"up", "--supervisor-profile", "x"}, false},
		{[]string{"--admiral-profile", "claude", "ls"}, nil, true},
		{[]string{"--admiral-profile"}, nil, true},
	} {
		got, err := topLevelProfile(c.in)
		if (err != nil) != c.err || (!c.err && !reflect.DeepEqual(got, c.want)) {
			t.Errorf("%v: got %v err %v", c.in, got, err)
		}
	}
	na, prof, err := parseUpArgs([]string{"--admiral-profile", "claude", "--no-attach"})
	if err != nil || !na || prof != "claude" {
		t.Errorf("up flags: %v %q %v", na, prof, err)
	}
	if na, prof, err := parseUpArgs([]string{"--no-attach"}); err != nil || !na || prof != "" {
		t.Errorf("--no-attach alone: %v %q %v", na, prof, err)
	}
	if _, prof, _ := parseUpArgs([]string{"--supervisor-profile", "x"}); prof != "x" {
		t.Errorf("deprecated alias: %q", prof)
	}
}

func TestAdmiralMCPArgs(t *testing.T) {
	got := admiralMCPArgs(tools.Profile{Patterns: "claude"}, "/opt/fleet")
	want := []string{"--mcp-config", `{"mcpServers":{"fleet":{"args":["mcp","--admiral","main"],"command":"/opt/fleet","type":"stdio"}}}`, "--allowedTools=mcp__fleet"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("claude: %v", got)
	}
	if got := admiralMCPArgs(tools.Profile{Patterns: "copilot"}, "x"); !reflect.DeepEqual(got, supervisorMCPArgs(tools.Profile{Patterns: "copilot"})) {
		t.Errorf("copilot: %v", got)
	}
	if got := admiralMCPArgs(tools.Profile{Patterns: "agy"}, "x"); got != nil {
		t.Errorf("agy: %v", got)
	}
}

// The boot prompt rides on prompt_args: copilot still ends with -i <boot>
// after the lean flags, claude gets it as the last positional argument.
func TestAdmiralLaunchBootPrompt(t *testing.T) {
	f := testFleet(t)
	sl, err := supervisorCmd(f, "copilot", supervisor.BootPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sl.Cmd, "--disable-builtin-mcps") || !strings.HasSuffix(sl.Cmd, "-i "+strings.ReplaceAll(tools.Quote(supervisor.BootPrompt), "'", `'\''`)+"'") || sl.Paste != "" {
		t.Errorf("copilot: %s (paste %q)", sl.Cmd, sl.Paste)
	}
	if i, j := strings.Index(sl.Cmd, "--disable-builtin-mcps"), strings.Index(sl.Cmd, " -i "); j < i {
		t.Errorf("-i must come after the lean flags: %s", sl.Cmd)
	}
	sl, _ = supervisorCmd(f, "claude", supervisor.BootPrompt)
	if !strings.Contains(sl.Cmd, "--mcp-config") || !strings.Contains(sl.Cmd, "--allowedTools=mcp__fleet "+strings.ReplaceAll(tools.Quote(supervisor.BootPrompt), "'", `'\''`)) {
		t.Errorf("claude: %s", sl.Cmd)
	}
	sl, _ = supervisorCmd(f, "fake", supervisor.BootPrompt)
	if strings.Contains(sl.Cmd, "fleet_brief") || sl.Paste != supervisor.BootPrompt {
		t.Errorf("no prompt_args: paste instead: %s %q", sl.Cmd, sl.Paste)
	}
	sl, _ = admiralLaunch(f, "copilot", "sess-9", "")
	if !strings.Contains(sl.Cmd, "--resume sess-9") || strings.Contains(sl.Cmd, "--session-id") || sl.ID != "sess-9" {
		t.Errorf("resume: %s", sl.Cmd)
	}
}

func TestSwapHandoff(t *testing.T) {
	at := time.Date(2026, 11, 5, 9, 30, 0, 0, time.UTC)
	head := "Admiral profile swap at 2026-11-05 09:30: copilot → claude-personal. Previous session: abc (resume: copilot --resume abc)."
	if got := swapHandoff("copilot", "claude-personal", "abc", "copilot --resume abc", "open question: deploy?", "pane stuff", at); got != head+"\n\nopen question: deploy?" {
		t.Errorf("kept: %q", got)
	}
	got := swapHandoff("copilot", "claude-personal", "abc", "copilot --resume abc", "", "line 1\nline 2", at)
	if !strings.HasPrefix(got, head+"\n\n") || !strings.HasSuffix(got, "line 1\nline 2") {
		t.Errorf("tail: %q", got)
	}
	if got := swapHandoff("a", "b", "", "", "", "", at); !strings.Contains(got, "Previous session: unknown (resume: n/a).") {
		t.Errorf("no id: %q", got)
	}
	big := strings.Repeat("é line\n", 2000)
	for _, g := range []string{swapHandoff("a", "b", "x", "", big, "", at), swapHandoff("a", "b", "x", "", "", big, at)} {
		if len(g)+1 > handoffCap || !strings.HasPrefix(g, "Admiral profile swap at") || !utf8.ValidString(g) {
			t.Errorf("cap: %d bytes", len(g))
		}
	}
	if got := paneTail("a\nb\nc\n\n  \n", 2); got != "b\nc" {
		t.Errorf("paneTail %q", got)
	}
}

func TestPickModel(t *testing.T) {
	m := newPick([]string{"claude-personal", "copilot", "claude"}, "copilot")
	if m.cur != 1 {
		t.Fatalf("cursor must start on the active row: %d", m.cur)
	}
	v := m.View()
	if !strings.Contains(v, "❯ 2  copilot") || !strings.Contains(v, "● now · restart") || !strings.Contains(v, "new chat") || !strings.Contains(v, "⏎ switch · esc cancel") {
		t.Errorf("view:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil || m.chosen != "claude" {
		t.Errorf("enter: %q", m.chosen)
	}
	m = newPick([]string{"a", "b"}, "a")
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")}); cmd == nil || m.chosen != "b" {
		t.Errorf("digit: %q", m.chosen)
	}
	m = newPick([]string{"a", "b"}, "a")
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc}); cmd == nil || m.chosen != "" {
		t.Errorf("esc: %q", m.chosen)
	}
	ps := map[string]tools.Profile{"copilot": {Patterns: "copilot"}, "claude": {Patterns: "claude"}, "agy": {Patterns: "agy"}}
	if got := pickable(ps); !reflect.DeepEqual(got, []string{"claude", "copilot"}) {
		t.Errorf("pickable %v", got)
	}
	a := pickPopupArgs(&fleet.Fleet{Exe: "/opt/my fleet", Profiles: ps})
	if strings.Join(a[:9], " ") != "display-popup -E -b rounded -w 44 -h 6 -T" || a[len(a)-1] != "'/opt/my fleet' admiral profile --pick" {
		t.Errorf("popup %q", a)
	}
	if got := swapShell("/opt/my fleet", "x'y"); got != `'/opt/my fleet' admiral profile 'x'\''y' >/dev/null 2>&1` {
		t.Errorf("swapShell %q", got)
	}
}

// admiralFleet is testFleet with an admiral pane in fleet:main running the
// fake profile and extra profiles: res (resumable), broken (dies at once)
// envy (sets an env var only the admiral may see) and flaky (resumable, but
// the resume dies at once).
func admiralFleet(t *testing.T) (*fleet.Fleet, string) {
	t.Helper()
	f := testFleet(t)
	t.Setenv("FLEET_ADMIRAL_START_WAIT", "400ms")
	toml := `[fake]
command = "sh"
args = ["-c", "sleep 300"]
launch_id = ["--sid", "{id}"]
patterns = "fake"
[sup2]
command = "sh"
args = ["-c", "sleep 301"]
patterns = "fake"
[res]
command = "sh"
args = ["-c", "sleep 302"]
launch_id = ["--sid", "{id}"]
resume = ["--resume", "{id}"]
patterns = "fake"
[flaky]
command = "sh"
args = ["-c", "case \"$0\" in --resume) exit 1;; esac; sleep 304"]
launch_id = ["--sid", "{id}"]
resume = ["--resume", "{id}"]
patterns = "fake"
[broken]
command = "false"
patterns = "fake"
[envy]
command = "sh"
args = ["-c", "sleep 303"]
launch_id = ["--sid", "{id}"]
env = { ADMIRAL_ONLY = "secret-dir" }
prompt_args = ["{prompt}"]
patterns = "fake"
`
	if err := os.WriteFile(filepath.Join(f.Home, "tools.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, err := tools.Load(f.Home)
	if err != nil {
		t.Fatal(err)
	}
	f.Profiles = ps
	sup, err := f.T.Run("new-session", "-d", "-s", fleet.Session, "-n", "main", "-P", "-F", "#{pane_id}", "sh -c 'echo admiral was here; sleep 300'")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	f.T.SetPaneOption(sup, "@role", "sup")
	f.T.SetPaneOption(sup, "remain-on-exit", "on")
	return f, sup
}

func paneFmt(t *testing.T, f *fleet.Fleet, pane, format string) string {
	t.Helper()
	out, _ := f.T.Run("display-message", "-p", "-t", pane, format)
	return out
}

func TestSwapAdmiralEndToEnd(t *testing.T) {
	f, sup := admiralFleet(t)
	started := time.Now().Add(-time.Hour)
	supervisor.Save(f.Home, supervisor.Session{SessionID: "old-id", Profile: "fake", PaneID: sup, Started: started, Generation: 1})
	// An agent on the board must not be touched by the swap.
	ag, err := f.Spawn("a1", "fake", f.Home, "")
	if err != nil {
		t.Fatal(err)
	}
	agPid := paneFmt(t, f, ag.PaneID, "#{pane_pid}")
	time.Sleep(200 * time.Millisecond) // let the pane draw its line

	if _, err := swapAdmiral(f, "nope"); err == nil || err.Error() != `no profile "nope"; kept fake` {
		t.Fatalf("unknown: %v", err)
	}

	msg, err := swapAdmiral(f, "envy")
	if err != nil || msg != "admiral → envy · gen 2 (handoff written)" {
		t.Fatalf("swap: %q %v", msg, err)
	}
	s, _ := supervisor.Load(f.Home)
	if s.Profile != "envy" || s.Generation != 2 || s.Previous != "old-id" || s.PaneID != sup {
		t.Errorf("session %+v", s)
	}
	if got := supervisor.LoadChoice(f.Home); got != "envy" {
		t.Errorf("choice %q", got)
	}
	if got := paneFmt(t, f, sup, "#{@fleet_admiral_profile}|#{@role}"); got != "envy|sup" {
		t.Errorf("pane options %q", got)
	}
	if sc := paneFmt(t, f, sup, "#{pane_start_command}"); !strings.Contains(sc, "sleep 303") || !strings.Contains(sc, "fleet_brief") {
		t.Errorf("start command %q", sc)
	}
	h, _, _ := supervisor.ReadHandoff(f.Home)
	if !strings.HasPrefix(h, "Admiral profile swap at ") || !strings.Contains(h, ": fake → envy. Previous session: old-id (resume: n/a).") || !strings.Contains(h, "admiral was here") {
		t.Errorf("handoff %q", h)
	}
	// Agents keep their own pane and never see the admiral's env.
	if got := paneFmt(t, f, ag.PaneID, "#{pane_pid}"); got != agPid {
		t.Errorf("agent pane respawned: %s -> %s", agPid, got)
	}
	if out, err := f.T.Run("show-environment", "-t", fleet.Session, "ADMIRAL_ONLY"); err == nil {
		t.Errorf("admiral env leaked into the session: %q", out)
	}
	if b := f.Store.Load().Agents["a1"]; b == nil || b.Profile != "fake" || b.PaneID != ag.PaneID {
		t.Errorf("agent board entry changed: %+v", b)
	}

	// A profile that dies at once: envy can't resume, so a fresh envy admiral
	// comes back and session.json records it; the choice is left alone.
	envyID := s.SessionID
	_, err = swapAdmiral(f, "broken")
	if err == nil || err.Error() != "admiral profile broken failed to start; kept envy" {
		t.Fatalf("broken: %v", err)
	}
	s, _ = supervisor.Load(f.Home)
	if s.Profile != "envy" || s.SessionID == "" || s.SessionID == envyID || s.Previous != envyID || s.Generation != 3 {
		t.Errorf("fresh restore not recorded: %+v (old id %s)", s, envyID)
	}
	if sc := paneFmt(t, f, sup, "#{pane_start_command}"); !strings.Contains(sc, "--sid "+s.SessionID) {
		t.Errorf("restored command %q", sc)
	}
	if got := supervisor.LoadChoice(f.Home); got != "envy" {
		t.Errorf("choice after failure %q", got)
	}
	if paneFmt(t, f, sup, "#{pane_dead}") != "0" || !strings.Contains(paneFmt(t, f, sup, "#{pane_start_command}"), "sleep 303") {
		t.Errorf("old admiral not restored: %q", paneFmt(t, f, sup, "#{pane_start_command}"))
	}
	if got := paneFmt(t, f, sup, "#{@fleet_admiral_profile}"); got != "envy" {
		t.Errorf("border profile after restore %q", got)
	}
	if h, _, _ := supervisor.ReadHandoff(f.Home); !strings.Contains(h, "envy → broken") {
		t.Errorf("failed swap must leave its handoff: %q", h)
	}
	// The retry must not keep the failed swap's automatic handoff.
	if _, err := swapAdmiral(f, "sup2"); err != nil {
		t.Fatal(err)
	}
	if h, _, _ := supervisor.ReadHandoff(f.Home); strings.Contains(h, "envy → broken") || !strings.Contains(h, ": envy → sup2.") {
		t.Errorf("retry reused the auto handoff: %q", h)
	}
}

// A failed swap from a profile without resume records the fresh old admiral.
func TestFailedSwapFreshRestore(t *testing.T) {
	f, sup := admiralFleet(t)
	supervisor.Save(f.Home, supervisor.Session{SessionID: "old-id", Profile: "fake", PaneID: sup, Started: time.Now().Add(-time.Hour), Generation: 1})
	if _, err := swapAdmiral(f, "broken"); err == nil {
		t.Fatal("broken started")
	}
	s, _ := supervisor.Load(f.Home)
	if s.Profile != "fake" || s.SessionID == "" || s.SessionID == "old-id" || s.Previous != "old-id" || s.Generation != 2 || s.PaneID != sup {
		t.Errorf("session %+v", s)
	}
	if got := supervisor.LoadChoice(f.Home); got != "" {
		t.Errorf("choice must stay unset: %q", got)
	}
	if paneFmt(t, f, sup, "#{pane_dead}") != "0" {
		t.Error("pane dead")
	}
}

// A resumable old admiral comes back on its own session: nothing is saved.
func TestFailedSwapResumeRestore(t *testing.T) {
	f, sup := admiralFleet(t)
	supervisor.Save(f.Home, supervisor.Session{SessionID: "sess-1", Profile: "res", PaneID: sup, Started: time.Now().Add(-time.Hour), Generation: 4})
	before, _ := os.ReadFile(filepath.Join(supervisor.Dir(f.Home), "session.json"))
	if _, err := swapAdmiral(f, "broken"); err == nil {
		t.Fatal("broken started")
	}
	after, _ := os.ReadFile(filepath.Join(supervisor.Dir(f.Home), "session.json"))
	if string(before) != string(after) {
		t.Errorf("session.json changed:\n%s\n%s", before, after)
	}
	if sc := paneFmt(t, f, sup, "#{pane_start_command}"); paneFmt(t, f, sup, "#{pane_dead}") != "0" || !strings.Contains(sc, "--resume sess-1") {
		t.Errorf("not resumed: %q", sc)
	}
}

// When the resume itself dies (a failed restart, or a restore after a failed
// swap) a fresh old-profile admiral starts with a handoff and is recorded.
func TestResumeDiesFreshFallback(t *testing.T) {
	f, sup := admiralFleet(t)
	supervisor.Save(f.Home, supervisor.Session{SessionID: "sess-1", Profile: "flaky", PaneID: sup, Started: time.Now().Add(-time.Hour), Generation: 3})
	err := cmdAdmiralRestart(f, nil)
	if err == nil || !strings.Contains(err.Error(), "could not resume") || strings.Contains(err.Error(), "restore failed") {
		t.Fatalf("restart: %v", err)
	}
	s, _ := supervisor.Load(f.Home)
	if s.Profile != "flaky" || s.SessionID == "" || s.SessionID == "sess-1" || s.Previous != "sess-1" || s.Generation != 4 {
		t.Errorf("session %+v", s)
	}
	sc := paneFmt(t, f, sup, "#{pane_start_command}")
	if paneFmt(t, f, sup, "#{pane_dead}") != "0" || !strings.Contains(sc, "--sid "+s.SessionID) {
		t.Errorf("fresh admiral not running: %q", sc)
	}
	if h, _, _ := supervisor.ReadHandoff(f.Home); !strings.HasPrefix(h, swapPrefix) || !strings.Contains(h, "flaky → flaky. Previous session: sess-1") {
		t.Errorf("fallback handoff %q", h)
	}

	// Swap to broken: the resume restore of flaky dies too, so fresh again.
	freshID := s.SessionID
	if _, err := swapAdmiral(f, "broken"); err == nil || strings.Contains(err.Error(), "restore failed") {
		t.Fatalf("broken: %v", err)
	}
	s, _ = supervisor.Load(f.Home)
	if s.Profile != "flaky" || s.SessionID == freshID || s.Previous != freshID || s.Generation != 5 || paneFmt(t, f, sup, "#{pane_dead}") != "0" {
		t.Errorf("after failed swap: %+v", s)
	}
}

func TestKeptHandoff(t *testing.T) {
	home := t.TempDir()
	f := &fleet.Fleet{Home: home}
	old := supervisor.Session{Started: time.Now().Add(-time.Minute)}
	supervisor.WriteHandoff(home, "open: deploy")
	if got := keptHandoff(f, old); got != "open: deploy" {
		t.Errorf("own handoff: %q", got)
	}
	supervisor.WriteHandoff(home, swapPrefix+" 2026-11-05 09:30: a → b. Previous session: x (resume: n/a).")
	if got := keptHandoff(f, old); got != "" {
		t.Errorf("auto handoff kept: %q", got)
	}
	if got := keptHandoff(f, supervisor.Session{Started: time.Now().Add(time.Minute)}); got != "" {
		t.Errorf("older handoff kept: %q", got)
	}
}

func TestRestartResumesSameSession(t *testing.T) {
	f, sup := admiralFleet(t)
	started := time.Now().Add(-time.Hour).Round(time.Second)
	supervisor.Save(f.Home, supervisor.Session{SessionID: "sess-1", Profile: "res", PaneID: sup, Started: started, Generation: 3, Previous: "p0"})
	pid0 := paneFmt(t, f, sup, "#{pane_pid}")
	if err := cmdAdmiralRestart(f, nil); err != nil {
		t.Fatal(err)
	}
	if pid := paneFmt(t, f, sup, "#{pane_pid}"); pid == pid0 {
		t.Error("pane not respawned")
	}
	if sc := paneFmt(t, f, sup, "#{pane_start_command}"); !strings.Contains(sc, "--resume sess-1") || strings.Contains(sc, "fleet_brief") {
		t.Errorf("restart command %q", sc)
	}
	s, _ := supervisor.Load(f.Home)
	if s.SessionID != "sess-1" || s.Generation != 3 || s.Previous != "p0" || !s.Started.Equal(started) || s.Profile != "res" {
		t.Errorf("session %+v", s)
	}
	if got := supervisor.LoadChoice(f.Home); got != "res" {
		t.Errorf("choice %q", got)
	}
	if msg, err := swapAdmiral(f, "res"); err != nil || msg != "admiral restarted on res (resumed sess-1)" {
		t.Errorf("profile <current>: %q %v", msg, err)
	}
}

// fleet admiral profile with no name lists the profiles, the active marked.
func TestProfileList(t *testing.T) {
	f, _ := admiralFleet(t)
	got := profileList(f, "res")
	if !strings.HasPrefix(got, "admiral profile: res\n") || !strings.Contains(got, "\n* res\n") || !strings.Contains(got, "\n  fake\n") {
		t.Errorf("%s", got)
	}
}

func TestAdmiralMCPConfigCarriesName(t *testing.T) {
	got := admiralMCPArgsOf(tools.Profile{Patterns: "claude"}, "/opt/fleet", "", "print")
	if len(got) < 2 || !strings.Contains(got[1], `"args":["mcp","--admiral","print"]`) {
		t.Errorf("claude: %v", got)
	}
	home := t.TempDir()
	got = admiralMCPArgsOf(tools.Profile{Patterns: "copilot"}, "/opt/fleet", home, "print")
	path, err := writeAdmiralMCP(home, "/opt/fleet", "print")
	if err != nil || path != filepath.Join(home, "admirals", "print", ".mcp.json") {
		t.Fatalf("write: %q %v", path, err)
	}
	if strings.Contains(strings.Join(got, " "), "--additional-mcp-config") {
		t.Fatalf("copilot args layer a second fleet server: %v", got)
	}
	b, err := os.ReadFile(path)
	if err != nil || !regexp.MustCompile(`"--admiral",\s*"print"`).Match(b) {
		t.Errorf("copilot config: %s %v", b, err)
	}
}

// A non-main copilot admiral starts in its own dir, whose .mcp.json is its
// only fleet server; main keeps the shared dir.
func TestCopilotAdmiralStartsInOwnDir(t *testing.T) {
	f, _ := admiralFleet(t)
	f.Profiles["cp"] = tools.Profile{Command: "sh", Args: []string{"-c", "sleep 305"}, Patterns: "copilot"}
	sl, err := admiralLaunchOf(f, "print", "cp", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := supervisor.DirOf(f.Home, "print"); sl.Dir != want {
		t.Errorf("print dir %q, want %q", sl.Dir, want)
	}
	if _, err := os.Stat(filepath.Join(sl.Dir, ".mcp.json")); err != nil {
		t.Errorf("no .mcp.json in the admiral's dir: %v", err)
	}
	m, err := admiralLaunchOf(f, supervisor.Main, "cp", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Dir == sl.Dir {
		t.Errorf("main moved into print's dir %q", m.Dir)
	}
}
