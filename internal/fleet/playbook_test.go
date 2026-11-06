package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/playbook"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/tools"
)

// argvFleet builds a Fleet on a fleet-test-* socket with a profile "pa" whose
// pane writes its prompt argument to <home>/argv.txt and then idles. config is
// the FLEET_CONFIG body ("" = no file).
func argvFleet(t *testing.T, config string) (*Fleet, string) {
	t.Helper()
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	out := filepath.Join(home, "argv.txt")
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[pa]
command = "sh"
args = ["-c", "printf '%s' \"$1\" > `+out+`; cat", "sh"]
prompt_args = ["{prompt}"]
patterns = "fake"
`), 0o644)
	cfg := filepath.Join(home, "no-config.toml")
	if config != "" {
		cfg = filepath.Join(home, "config.toml")
		os.WriteFile(cfg, []byte(config), 0o644)
	}
	t.Setenv("FLEET_CONFIG", cfg)
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	return f, out
}

func readArg(t *testing.T, path string) string {
	t.Helper()
	for i := 0; i < 50; i++ {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return string(b)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("pane never wrote %s", path)
	return ""
}

// A dir outside any git repo gets no worktree, so the default effort is
// light (ADR-57).
func TestSpawnPrependsPlaybook(t *testing.T) {
	f, out := argvFleet(t, "")
	dir := t.TempDir() // no .fleet/worker.md here, and f.Home has no worker.md
	if _, err := f.Spawn("pb1", "pa", dir, "fix the bug"); err != nil {
		t.Fatal(err)
	}
	got := readArg(t, out)
	want := playbook.Compose(playbook.Text(playbook.Light), "fix the bug")
	if got != want {
		t.Fatalf("launch prompt:\n%.200q\nwant\n%.200q", got, want)
	}
	if !strings.HasPrefix(got, "# Fleet worker playbook") || !strings.HasSuffix(got, "## Your task\n\nfix the bug") {
		t.Fatalf("playbook not before the task: %.80q", got)
	}
	if g := f.Store.Load().Agents["pb1"].Goal; g != "fix the bug" {
		t.Errorf("board goal %q, want the user's prompt", g)
	}
}

// The suite is hermetic: a spawn into a temp dir reads no layer from above
// the temp root, even when the home directory the walk stops at is not an
// ancestor of it (a symlinked home, or a test that sets HOME) and TMPDIR sits
// under the user's real home next to ~/.fleet/worker.md (ADR-62).
func TestSpawnIgnoresLayersAboveTheTempRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f, out := argvFleet(t, "")
	if _, err := f.Spawn("pb12", "pa", t.TempDir(), "fix the bug"); err != nil {
		t.Fatal(err)
	}
	if got, want := readArg(t, out), playbook.Compose(playbook.Text(playbook.Light), "fix the bug"); got != want {
		t.Fatalf("a layer outside the test leaked in:\n%.300q", got)
	}
}

func TestSpawnPlaybookOff(t *testing.T) {
	f, out := argvFleet(t, "[defaults]\nplaybook = false\n")
	if _, err := f.Spawn("pb2", "pa", t.TempDir(), "fix the bug"); err != nil {
		t.Fatal(err)
	}
	if got := readArg(t, out); got != "fix the bug" {
		t.Fatalf("launch prompt %q, want only the prompt", got)
	}
}

// The logged argv hides the whole composed prompt, and the byte count is the
// composed length (not the user's prompt length).
func TestSpawnPlaybookLogRedacted(t *testing.T) {
	logHome := t.TempDir()
	t.Setenv("FLEET_HOME", logHome)
	logx.Init() // first Init in this test binary, so the log lands in logHome
	logFile := filepath.Join(logHome, "logs", "fleet.log")
	if _, err := os.Stat(logFile); err != nil {
		t.Skip("logx already initialised elsewhere in this test binary")
	}
	f, out := argvFleet(t, "")
	if _, err := f.Spawn("pb3", "pa", t.TempDir(), "fix the bug"); err != nil {
		t.Fatal(err)
	}
	full := readArg(t, out)
	b, _ := os.ReadFile(logFile)
	var line string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, "msg=started") && strings.Contains(l, "agent=pb3") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no started line in log:\n%s", b)
	}
	if strings.Contains(line, "worker playbook") || strings.Contains(line, "fix the bug") {
		t.Fatalf("prompt leaked into the log: %s", line)
	}
	if want := fmt.Sprintf("<prompt:%d bytes>", len(full)); !strings.Contains(line, want) {
		t.Fatalf("log line lacks %s: %s", want, line)
	}
}

// A prompt too long for tmux new-window falls back to the paste path.
func TestLaunchArgvLongPromptPastes(t *testing.T) {
	f, _ := argvFleet(t, "")
	p := f.Profiles["pa"]
	v := tools.AgentVars("/nonexistent", "x", "")
	argv, embedded := launchArgv("x", p, v, tools.Launch{Prompt: "short"})
	if !embedded || argv[len(argv)-1] != "short" {
		t.Fatalf("short prompt not embedded: %q", argv)
	}
	long := strings.Repeat("y", MaxLaunchCmd+1)
	argv, embedded = launchArgv("x", p, v, tools.Launch{Prompt: long})
	if embedded {
		t.Fatal("long prompt still embedded")
	}
	if n := len(tools.ShellCommand(argv)); n > MaxLaunchCmd {
		t.Fatalf("launch command %d bytes", n)
	}
}

// End to end: the long prompt reaches the pane via paste and spawn succeeds.
func TestSpawnLongPromptViaPaste(t *testing.T) {
	f, _ := argvFleet(t, "[defaults]\nplaybook = false\nready_timeout = \"3s\"\n")
	long := strings.Repeat("z", 20000)
	if _, err := f.Spawn("pb4", "pa", t.TempDir(), long); err != nil {
		t.Fatalf("spawn with a 20KB prompt: %v", err)
	}
}

// ADR-57: the spawn's effort picks the playbook level, and an unknown one is
// refused before anything is started.
func TestSpawnEffortLevel(t *testing.T) {
	f, out := argvFleet(t, "")
	if _, err := f.SpawnWith(SpawnOpts{Effort: "pro"}, "pb5", "pa", t.TempDir(), "fix the bug"); err != nil {
		t.Fatal(err)
	}
	if got, want := readArg(t, out), playbook.Compose(playbook.Text(playbook.Pro), "fix the bug"); got != want {
		t.Fatalf("effort=pro prompt:\n%.120q\nwant\n%.120q", got, want)
	}
	if _, err := f.SpawnWith(SpawnOpts{Effort: " PRO "}, "pb9", "pa", t.TempDir(), "fix the bug"); err != nil {
		t.Fatal(err)
	}
	if got := f.Store.Load().Agents["pb9"].Effort; got != "pro" {
		t.Errorf("board kept effort %q, want the canonical name", got)
	}
	if _, err := f.SpawnWith(SpawnOpts{Effort: "full"}, "pb6", "pa", t.TempDir(), "x"); err == nil {
		t.Fatal("effort=full was accepted")
	}
	if _, ok := f.Store.Load().Agents["pb6"]; ok {
		t.Error("a refused effort still put the agent on the board")
	}
}

// Without a worktree the default is light; with one it is pro.
func TestEffortLevel(t *testing.T) {
	cases := []struct {
		effort   string
		worktree bool
		want     playbook.Level
	}{
		{"", false, playbook.Light},
		{"", true, playbook.Pro},
		{"standard", true, playbook.Standard},
		{"light", true, playbook.Light},
		{"nonsense", true, playbook.Pro},
		{"nonsense", false, playbook.Light},
	}
	for _, c := range cases {
		if got := effortLevel(c.effort, c.worktree); got != c.want {
			t.Errorf("effortLevel(%q, %v) = %q, want %q", c.effort, c.worktree, got, c.want)
		}
	}
}

// A queued agent carries its effort to the drain, the way it carries the
// worktree choice and the card (ADR-57).
func TestQueuedAgentKeepsEffort(t *testing.T) {
	f, out := argvFleet(t, "[defaults]\nmax_working = 1\n")
	if _, err := f.Spawn("pb7", "pa", t.TempDir(), "first"); err != nil {
		t.Fatal(err)
	}
	readArg(t, out)
	q, err := f.SpawnWith(SpawnOpts{Effort: "standard"}, "pb8", "pa", t.TempDir(), "second")
	if err != nil {
		t.Fatal(err)
	}
	if q.Status != "queued" {
		t.Fatalf("queued entry: status %q", q.Status)
	}
	if got := f.Store.Load().Agents["pb8"].Effort; got != "standard" {
		t.Fatalf("board kept effort %q", got)
	}
	os.Remove(out)
	var claimed *board.Agent
	f.Store.Update(func(b *board.Board) error { // what claimQueued does under the board lock
		b.Agents["pb8"].Status = "starting"
		c := *b.Agents["pb8"] // the drain launches from the reloaded entry
		claimed = &c
		return nil
	})
	f.startQueued(claimed)
	if got, want := readArg(t, out), playbook.Compose(playbook.Text(playbook.Standard), "second"); got != want {
		t.Fatalf("drained prompt:\n%.120q\nwant\n%.120q", got, want)
	}
}

// The default with a worktree is the full pro text, and a worktree git
// refused keeps it: that shared checkout is where the process matters most
// (ADR-57).
func TestSpawnDefaultsToProInARepo(t *testing.T) {
	f, out := argvFleet(t, "")
	if _, err := f.Spawn("pb10", "pa", repo(t), "fix the bug"); err != nil {
		t.Fatal(err)
	}
	if got, want := readArg(t, out), playbook.Compose(playbook.Text(playbook.Pro), "fix the bug"); got != want {
		t.Fatalf("repo spawn prompt:\n%.120q\nwant\n%.120q", got, want)
	}
	if w := f.Store.Load().Agents["pb10"].Worktree; w == nil {
		t.Fatal("no worktree, so this did not test the worktree default")
	}
	// A repo git refuses a worktree in (the branch is checked out there) is
	// still a code task, and that shared checkout needs the full process.
	taken := repo(t)
	git(t, taken, "checkout", "-q", "-b", "fleet/pb11")
	os.Remove(out)
	if _, err := f.Spawn("pb11", "pa", taken, "fix the bug"); err != nil {
		t.Fatal(err)
	}
	if got, want := readArg(t, out), playbook.Compose(playbook.Text(playbook.Pro), "fix the bug"); got != want {
		t.Fatalf("worktree fallback prompt:\n%.120q\nwant\n%.120q", got, want)
	}
	if w := f.Store.Load().Agents["pb11"].Worktree; w != nil {
		t.Fatalf("expected the worktree to be refused, got %+v", w)
	}
}

// A leader is told about its squad tools in its first prompt, whatever the
// effort level and even with the playbook off: the MCP instructions alone
// left a leader hunting for tools it could not name (ADR-58).
func TestLeaderPromptCarriesSquadOrientation(t *testing.T) {
	for _, tc := range []struct{ name, config, prompt string }{
		{"playbook on", "", "run the effort"},
		{"playbook off", "[defaults]\nplaybook = false\n", "run the effort"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, out := argvFleet(t, tc.config)
			if _, err := f.SpawnWith(SpawnOpts{Role: Role{Leader: true}}, "ld1", "pa", t.TempDir(), tc.prompt); err != nil {
				t.Fatal(err)
			}
			got := readArg(t, out)
			for _, want := range []string{"fleet_squad_spawn", "report to **you**", "state=blocked"} {
				if !strings.Contains(got, want) {
					t.Errorf("leader prompt lacks %q:\n%.400q", want, got)
				}
			}
			if tc.prompt != "" && !strings.HasSuffix(got, "## Your task\n\n"+tc.prompt) {
				t.Errorf("orientation not before the task: %.120q", got)
			}
		})
	}
}

// A plain agent gets none of it.
func TestNonLeaderPromptHasNoSquadOrientation(t *testing.T) {
	f, out := argvFleet(t, "")
	if _, err := f.Spawn("ld2", "pa", t.TempDir(), "fix the bug"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readArg(t, out), "fleet_squad_spawn") {
		t.Fatal("squad orientation in a non-leader prompt")
	}
}

// The context level follows the effort level; a leader uses the profile's
// leader entry when there is one (ADR-60).
func TestContextKey(t *testing.T) {
	withLeader := tools.Profile{ContextArgs: map[string][]string{"leader": nil, "light": nil}}
	cases := []struct {
		p      tools.Profile
		lvl    playbook.Level
		leader bool
		want   string
	}{
		{withLeader, playbook.Light, false, "light"},
		{withLeader, playbook.Light, true, "leader"},
		{tools.Profile{}, playbook.Standard, true, "standard"},
	}
	for _, c := range cases {
		if got := contextKey(c.p, c.lvl, c.leader); got != c.want {
			t.Errorf("contextKey(%q, leader=%v) = %q, want %q", c.lvl, c.leader, got, c.want)
		}
	}
}
