package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/graph"
	"github.com/sturlabragason/fleet/internal/watch"
)

// doctorTestEnv: a clean machine with tmux 3.8, a UTF-8 locale and
// ~/.local/bin on PATH, where only the commands in found exist.
func doctorTestEnv(t *testing.T, found ...string) doctorEnv {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(dir, "config.toml"))
	vars := map[string]string{"LANG": "en_US.UTF-8", "PATH": "/usr/bin:" + filepath.Join(dir, "u", ".local", "bin") + "/"}
	return doctorEnv{
		home: filepath.Join(dir, "fleet"), userHome: filepath.Join(dir, "u"), exe: "/x/fleet",
		getenv:      func(k string) string { return vars[k] },
		tmuxVersion: func() (string, error) { return "tmux 3.8", nil },
		look: func(cmd string) error {
			for _, f := range found {
				if f == cmd {
					return nil
				}
			}
			return errors.New("not found")
		},
	}
}

func levels(cs []doctorCheck) map[string]string {
	m := map[string]string{}
	for _, c := range cs {
		m[c.Name] = c.Level
	}
	return m
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorCleanMachine(t *testing.T) {
	env := doctorTestEnv(t, "copilot")
	cs := doctor(env)
	want := map[string]string{"tmux": "ok", "locale": "ok", "PATH": "ok", "board": "ok", "watcher": "ok", "config.toml": "ok", "tools.toml": "ok", "profile copilot": "ok"}
	if got := levels(cs); len(got) != len(want) {
		t.Fatalf("checks = %v, want %v", got, want)
	} else {
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s = %q, want %q (%v)", k, got[k], v, cs)
			}
		}
	}
	var out bytes.Buffer
	if err := writeDoctor(&out, cs, false); err != nil {
		t.Fatalf("exit = %v on no fail", err)
	}
	if !strings.Contains(out.String(), "ok    tmux: tmux 3.8 (fleet needs 3.3 or newer)") {
		t.Errorf("output:\n%s", out.String())
	}
}

// The admiral's missing command is a fail; a user profile's is a warn. Built-in
// profiles the user never defined (agy, vibe) are not checked.
func TestDoctorProfiles(t *testing.T) {
	env := doctorTestEnv(t, "claude")
	write(t, filepath.Join(env.home, "tools.toml"), "[mytool]\ncommand = \"mytool-cli\"\n")
	write(t, os.Getenv("FLEET_CONFIG"), "[profile.claude]\nmode = \"auto\"\n[profile.typo]\nmode = \"auto\"\n")
	got := levels(doctor(env))
	for k, v := range map[string]string{"profile copilot": "fail", "profile mytool": "warn", "profile claude": "ok", "profile typo": "warn"} {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (%v)", k, got[k], v, got)
		}
	}
	if _, ok := got["profile agy"]; ok {
		t.Errorf("checked a built-in profile the user never defined: %v", got)
	}
	if err := writeDoctor(&bytes.Buffer{}, doctor(env), true); !errors.Is(err, exitCode(1)) {
		t.Errorf("exit = %v, want exit 1", err)
	}
}

func TestDoctorBrokenConfigFiles(t *testing.T) {
	env := doctorTestEnv(t, "copilot")
	write(t, os.Getenv("FLEET_CONFIG"), "[defaults\n")
	write(t, filepath.Join(env.home, "tools.toml"), "[x\n")
	write(t, filepath.Join(env.home, "board.json"), "{nope")
	got := levels(doctor(env))
	if got["config.toml"] != "fail" || got["tools.toml"] != "fail" || got["profiles"] != "warn" || got["board"] != "fail" {
		t.Errorf("broken files: %v", got)
	}
}

func TestDoctorEnvironmentWarnings(t *testing.T) {
	env := doctorTestEnv(t, "copilot")
	env.getenv = func(k string) string {
		return map[string]string{"LANG": "en_US.UTF-8", "LC_ALL": "C", "PATH": "/usr/bin"}[k]
	}
	got := levels(doctor(env))
	if got["locale"] != "warn" || got["PATH"] != "warn" {
		t.Errorf("LC_ALL=C and no ~/.local/bin: %v", got)
	}
}

func TestCheckTmux(t *testing.T) {
	for v, want := range map[string]string{
		"tmux 3.8": "ok", "tmux 3.3a": "ok", "tmux next-3.6": "ok", "tmux 4.0": "ok",
		"tmux 3.2a": "fail", "tmux 2.9": "fail", "tmux master": "warn", "tmux openbsd-6.9": "warn", "": "fail",
	} {
		if got := checkTmux(func() (string, error) { return v, nil }).Level; got != want {
			t.Errorf("%q = %s, want %s", v, got, want)
		}
	}
	if got := checkTmux(func() (string, error) { return "", errors.New("no tmux") }); got.Level != "fail" || got.Fix == "" {
		t.Errorf("no tmux = %+v", got)
	}
}

// A watcher that left a heartbeat but stopped refreshing is a warning.
func TestDoctorStaleWatcher(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	if err := watch.WriteBeat(home, watch.Beat{PID: os.Getpid(), Started: now.Add(-time.Hour), At: now, OK: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if c := checkWatcher(home, now); c.Level != levelWarn {
		t.Errorf("stale refresh: %+v", c)
	}
	watch.WriteBeat(home, watch.Beat{PID: os.Getpid(), Started: now.Add(-time.Hour), At: now, OK: now})
	if c := checkWatcher(home, now); c.Level != levelOK {
		t.Errorf("live watcher: %+v", c)
	}
}

func TestCheckOrphans(t *testing.T) {
	if c := checkOrphans(func() ([]fleet.Orphan, error) { return nil, fleet.ErrNoOwnerTags }); c.Level != levelInfo {
		t.Errorf("no owner tags: %+v", c)
	}
	c := checkOrphans(func() ([]fleet.Orphan, error) { return nil, nil })
	if c.Level != levelOK {
		t.Errorf("no orphans: %+v", c)
	}
	c = checkOrphans(func() ([]fleet.Orphan, error) {
		return []fleet.Orphan{{Agent: "a1", PID: 42, Comm: "node", Mem: 2 << 30}}, nil
	})
	if c.Level != levelWarn || !strings.Contains(c.Detail, "a1 pid 42 (node") || c.Fix == "" {
		t.Errorf("one orphan: %+v", c)
	}
	c = checkOrphans(func() ([]fleet.Orphan, error) {
		return []fleet.Orphan{{Agent: "deck", PID: 7, Comm: "soffice.bin", Mem: 2 << 30, Spared: true, Docs: []string{"/w/a.pptx", "/w/b.odp"}}}, nil
	})
	if !strings.Contains(c.Detail, "documents open: /w/a.pptx, /w/b.odp") || strings.Contains(c.Fix, "kill") ||
		!strings.Contains(c.Fix, "close the documents first") {
		t.Errorf("LibreOffice orphan: %+v", c)
	}
}

func TestCheckCompanions(t *testing.T) {
	spec := companion.Spec{Name: "cass", Feature: "session search", Hint: "install cass"}
	cs := checkCompanions([]companion.Status{
		{Spec: spec, Path: "/bin/cass", Source: "PATH", Version: "0.11.0"},
		{Spec: spec, Path: "/opt/cass", Source: "$FLEET_CASS"},
		{Spec: spec, Source: "config", Err: &companion.Error{Spec: spec, Err: companion.ErrOff}},
		{Spec: spec, Err: &companion.Error{Spec: spec, Err: companion.ErrMissing}},
	})
	want := []doctorCheck{
		{Name: "optional: cass", Level: levelOK, Detail: "/bin/cass 0.11.0"},
		{Name: "optional: cass", Level: levelOK, Detail: "/opt/cass (from $FLEET_CASS)"},
		{Name: "optional: cass", Level: levelInfo, Detail: "off by config: session search is off"},
		{Name: "optional: cass", Level: levelInfo, Detail: "cass is not installed: session search is off", Fix: "install cass"},
	}
	if !reflect.DeepEqual(cs, want) {
		t.Errorf("got %+v\nwant %+v", cs, want)
	}
	var buf bytes.Buffer
	if err := writeDoctor(&buf, cs, false); err != nil {
		t.Errorf("an optional tool must never fail doctor: %v", err)
	}
}

func TestCheckGraphs(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	if _, ok := checkGraphs(nil, "/home/u", now); ok {
		t.Error("no graphs should add no check")
	}
	c, ok := checkGraphs([]graph.Stamp{{Root: "/home/u/git/x", BuiltAt: now.Add(-3 * time.Hour)}}, "/home/u", now)
	want := doctorCheck{Name: "code graphs", Level: levelInfo, Detail: "~/git/x 3h0m0s old"}
	if !ok || c != want {
		t.Errorf("got %+v, want %+v", c, want)
	}
	env := doctorTestEnv(t, "copilot")
	write(t, filepath.Join(env.home, "graphs", "k", "fleet-graph.json"), `{"root":"/r","built_at":"2026-01-02T03:04:05Z","schema_version":1}`)
	if got := levels(doctor(env))["code graphs"]; got != levelInfo {
		t.Errorf("doctor code graphs = %q", got)
	}
}
