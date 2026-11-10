package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	want := map[string]string{"tmux": "ok", "locale": "ok", "PATH": "ok", "config.toml": "ok", "tools.toml": "ok", "profile copilot": "ok"}
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
	if err := writeDoctor(&bytes.Buffer{}, doctor(env), true); err != exitCode(1) {
		t.Errorf("exit = %v, want exit 1", err)
	}
}

func TestDoctorBrokenConfigFiles(t *testing.T) {
	env := doctorTestEnv(t, "copilot")
	write(t, os.Getenv("FLEET_CONFIG"), "[defaults\n")
	write(t, filepath.Join(env.home, "tools.toml"), "[x\n")
	got := levels(doctor(env))
	if got["config.toml"] != "fail" || got["tools.toml"] != "fail" || got["profiles"] != "warn" {
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
