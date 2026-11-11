package prefs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/status"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_CONFIG", p)
	return p
}

func TestMissingFileIsNeutral(t *testing.T) {
	t.Setenv("FLEET_CONFIG", filepath.Join(t.TempDir(), "nope.toml"))
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := p.For("copilot"); !reflect.DeepEqual(got, Neutral()) {
		t.Errorf("%+v want %+v", got, Neutral())
	}
	if p.ToolsDoc() == nil || len(p.ToolsDoc()) != 0 {
		t.Errorf("tools doc %v", p.ToolsDoc())
	}
}

func TestPathPrecedence(t *testing.T) {
	t.Setenv("FLEET_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := Path(); got != "/xdg/fleet/config.toml" {
		t.Errorf("xdg path %q", got)
	}
	t.Setenv("FLEET_CONFIG", "/tmp/x.toml")
	if got := Path(); got != "/tmp/x.toml" {
		t.Errorf("env path %q", got)
	}
}

func TestDefaultsAndProfileOverride(t *testing.T) {
	write(t, `
[defaults]
mode = "autopilot"
auto_submit = false
ready_timeout = "5s"
profile = " my-copilot "

[profile.copilot]
mode = "plan"
extra_args = ["--reasoning-effort", "high"]

[profile.my-copilot]
command = "copilot"
patterns = "copilot"
`)
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := p.DefaultProfile(); got != "my-copilot" {
		t.Errorf("default profile %q", got)
	}
	// [defaults] reaches a profile with no section of its own.
	got := p.For("claude")
	if got.Mode != "autopilot" || got.AutoSubmit || got.ReadyTimeout != 5*time.Second {
		t.Errorf("defaults: %+v", got)
	}
	// [profile.x] wins over [defaults], and inherits what it doesn't set.
	got = p.For("copilot")
	if got.Mode != "plan" || got.AutoSubmit || got.ReadyTimeout != 5*time.Second {
		t.Errorf("profile override: %+v", got)
	}
	if !reflect.DeepEqual(got.ExtraArgs, []string{"--reasoning-effort", "high"}) {
		t.Errorf("extra_args %v", got.ExtraArgs)
	}
	// Profile sections are also a tools override layer.
	d := p.ToolsDoc()
	if _, ok := d["my-copilot"]; !ok {
		t.Errorf("tools doc missing my-copilot: %v", d)
	}
	if _, ok := d["defaults"]; ok {
		t.Error("[defaults] leaked into the tools doc")
	}
}

func TestBadTOMLNamesTheFile(t *testing.T) {
	path := write(t, "[defaults]\nthis is not toml\n")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want it to name %s", err, path)
	}
}

func TestNilPrefsIsNeutral(t *testing.T) {
	var p *Prefs
	if got := p.For("copilot"); !reflect.DeepEqual(got, Neutral()) {
		t.Errorf("%+v", got)
	}
	if p.ToolsDoc() != nil {
		t.Error("nil ToolsDoc")
	}
}

func TestEnvMergesAndMaxWorking(t *testing.T) {
	write(t, "[defaults]\nmax_working = 2\nenv = { A = \"1\", B = \"2\" }\n[profile.copilot]\nenv = { B = \"3\" }\n")
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := p.For("copilot").Env; !reflect.DeepEqual(got, map[string]string{"A": "1", "B": "3"}) {
		t.Errorf("copilot env %v", got)
	}
	if got := p.For("claude").Env; !reflect.DeepEqual(got, map[string]string{"A": "1", "B": "2"}) {
		t.Errorf("claude env %v", got)
	}
	if p.MaxWorking() != 2 {
		t.Errorf("max_working %d", p.MaxWorking())
	}
	write(t, "[defaults]\nmode = \"\"\n")
	if p, _ := Load(); p.MaxWorking() != DefaultMaxWorking {
		t.Errorf("default max_working %d", p.MaxWorking())
	}
	write(t, "[defaults]\nmax_working = -1\n")
	if _, err := Load(); err == nil {
		t.Error("negative max_working accepted")
	}
}

func TestPlaybookPref(t *testing.T) {
	if !Neutral().Playbook {
		t.Fatal("playbook should default to on")
	}
	write(t, "[defaults]\nplaybook = false\n")
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if p.For("copilot").Playbook {
		t.Error("[defaults] playbook = false ignored")
	}
	write(t, "[profile.x]\nplaybook = false\n")
	if p, err = Load(); err != nil {
		t.Fatal(err)
	}
	if p.For("x").Playbook {
		t.Error("[profile.x] playbook = false ignored")
	}
	if !p.For("y").Playbook {
		t.Error("other profile lost the default")
	}
}

func TestDeadPaneGrace(t *testing.T) {
	write(t, `
[defaults]
dead_pane_grace = "2m"

[profile.zero]
dead_pane_grace = "0"

[profile.off]
dead_pane_grace = " Off "

[profile.bad]
dead_pane_grace = "-1m"
`)
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if Neutral().DeadPaneGrace != 5*time.Minute {
		t.Errorf("default %v", Neutral().DeadPaneGrace)
	}
	for prof, want := range map[string]time.Duration{"copilot": 2 * time.Minute, "zero": 0, "off": GraceOff, "bad": 2 * time.Minute} {
		if got := p.For(prof).DeadPaneGrace; got != want {
			t.Errorf("%s: %v want %v", prof, got, want)
		}
	}
}

func TestStuckAfterAndAction(t *testing.T) {
	write(t, `
[defaults]
stuck_after = "2m"

[profile.off]
stuck_after = " Off "

[profile.bad]
stuck_after = "0"

[profile.quick]
stuck_after = "30s"
stuck_action = " Interrupt "

[profile.typo]
stuck_action = "pause"
`)
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if Neutral().StuckAfter != status.DefaultStuckAfter || Neutral().StuckAction != StuckNone {
		t.Errorf("default %v %q", Neutral().StuckAfter, Neutral().StuckAction)
	}
	for prof, want := range map[string]time.Duration{"copilot": 2 * time.Minute, "off": 0, "bad": 2 * time.Minute, "quick": 30 * time.Second} {
		if got := p.For(prof).StuckAfter; got != want {
			t.Errorf("%s: stuck_after %v, want %v", prof, got, want)
		}
	}
	// A typo must never start pressing keys in an agent's pane.
	for prof, want := range map[string]string{"copilot": StuckNone, "quick": StuckInterrupt, "typo": StuckNone} {
		if got := p.For(prof).StuckAction; got != want {
			t.Errorf("%s: stuck_action %q, want %q", prof, got, want)
		}
	}
}

func TestThemePref(t *testing.T) {
	write(t, "[defaults]\ntheme = \"phosphor\"\n")
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Theme(); got != "phosphor" {
		t.Errorf("Theme() = %q, want phosphor", got)
	}
	write(t, "[defaults]\nmode = \"plan\"\n")
	if p, err = Load(); err != nil {
		t.Fatal(err)
	}
	if got := p.Theme(); got != "" {
		t.Errorf("unset Theme() = %q, want the default", got)
	}
}

func TestDriver(t *testing.T) {
	write(t, "[defaults]\ndriver = \"ACP\"\n[profile.claude]\ndriver = \"tmux\"\n[profile.agy]\ndriver = \"tmuxx\"\n")
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for prof, want := range map[string]string{"copilot": DriverACP, "claude": DriverTmux, "agy": DriverACP} {
		if got := p.For(prof).Driver; got != want {
			t.Errorf("%s: driver %q want %q", prof, got, want)
		}
	}
	// Unset in config.toml: the tool profile's default decides (ADR-74).
	if Neutral().Driver != "" || (*Prefs)(nil).For("x").Driver != "" {
		t.Error("config sets a driver nobody asked for")
	}
}

// fence_writes defaults on and is overridable per profile (ADR-79).
func TestFenceWritesPref(t *testing.T) {
	if !Neutral().FenceWrites {
		t.Fatal("fence_writes should default to on")
	}
	write(t, "[profile.claude]\nfence_writes = false\n")
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if p.For("claude").FenceWrites || !p.For("other").FenceWrites {
		t.Errorf("claude %v, other %v", p.For("claude").FenceWrites, p.For("other").FenceWrites)
	}
}

// fleet config explain: each key's effective value and the layer that set it.
func TestExplain(t *testing.T) {
	write(t, "[defaults]\nmode = \"auto\"\nstuck_after = \"off\"\nenv = { A = \"secret\" }\n[profile.x]\nmode = \"plan\"\nready_timeout = \"0s\"\nenv = { B = \"1\" }\n")
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range p.Explain("x") {
		got[s.Key] = s.Value + " " + s.Source
	}
	for k, want := range map[string]string{
		"mode":          "plan [profile.x]",
		"stuck_after":   "off [defaults]",
		"env.A":         "(set) [defaults]",
		"env.B":         "(set) [profile.x]",
		"ready_timeout": "15s built-in", // 0s is refused, so not the profile's
		"worktree":      "true built-in",
	} {
		if got[k] != want {
			t.Errorf("%s: %q want %q", k, got[k], want)
		}
	}
	if got := p.Explain("y")[0]; got.Value+" "+got.Source != "auto [defaults]" {
		t.Errorf("y mode: %+v", got)
	}
}
