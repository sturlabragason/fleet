package main

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// testFleet builds a Fleet on a throwaway socket with state in a temp dir and
// a "fake" admiral profile that just sleeps.
func testFleet(t *testing.T) *fleet.Fleet {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COPILOT_HOME", t.TempDir())
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	t.Setenv("FLEET_ADMIRAL_PROFILE", "fake")
	t.Setenv("FLEET_ADMIRAL_DIR", home)
	t.Setenv("FLEET_SUPERVISOR_PROFILE", "")
	t.Setenv("FLEET_SUPERVISOR_DIR", "")
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte("[fake]\ncommand = \"sh\"\nargs = [\"-c\", \"sleep 300\"]\npatterns = \"fake\"\n[sup2]\ncommand = \"sh\"\nargs = [\"-c\", \"sleep 301\"]\npatterns = \"fake\"\n"), 0o644)
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := fleet.New(tc, home, "/nonexistent/fleet")
	if err != nil {
		t.Fatal(err)
	}
	// copilot and claude need not be installed where the tests run.
	f.LookCmd = func(string) error { return nil }
	return f
}

func TestResetWithoutSupPane(t *testing.T) {
	f := testFleet(t)
	if _, err := resetSupervisor(f, false); err == nil || err.Error() != "no admiral pane; run fleet up" {
		t.Fatalf("err %v", err)
	}
}

func TestResetRespawnsSupervisor(t *testing.T) {
	f := testFleet(t)
	sup, err := f.T.Run("new-session", "-d", "-s", fleet.Session, "-n", "main", "-P", "-F", "#{pane_id}", "sh -c 'sleep 300'")
	if err != nil {
		t.Fatal(err)
	}
	f.T.SetPaneOption(sup, "@role", "sup")
	f.T.SetPaneOption(sup, "remain-on-exit", "on")
	f.T.SetPaneOption(sup, "@fleet_ctx", "74%")
	pid0, _ := f.T.Run("display-message", "-p", "-t", sup, "#{pane_pid}")
	started := time.Now().Add(-time.Hour)
	supervisor.Save(f.Home, supervisor.Session{SessionID: "old-id", Profile: "sup2", PaneID: sup, Started: started, Generation: 1})

	gen, err := resetSupervisor(f, false)
	if err != nil {
		t.Fatal(err)
	}
	if gen != 2 {
		t.Errorf("gen %d", gen)
	}
	pid1, _ := f.T.Run("display-message", "-p", "-t", sup, "#{pane_pid}")
	if pid1 == "" || pid1 == pid0 {
		t.Errorf("pane not respawned: %s -> %s", pid0, pid1)
	}
	if sc, _ := f.T.Run("display-message", "-p", "-t", sup, "#{pane_start_command}"); !strings.Contains(sc, "sleep 301") {
		t.Errorf("reset must reuse the session's profile: %q", sc)
	}
	if role, _ := f.T.Run("display-message", "-p", "-t", sup, "#{@role}|#{@fleet_ctx}"); role != "sup|" {
		t.Errorf("pane options after reset: %q", role)
	}
	s, ok := supervisor.Load(f.Home)
	if !ok || s.Generation != 2 || s.Previous != "old-id" || s.PaneID != sup || !s.Started.After(started) || s.Profile != "sup2" {
		t.Errorf("session %+v", s)
	}
	h, _, ok := supervisor.ReadHandoff(f.Home)
	if !ok || !strings.Contains(h, "Manual reset at") || !strings.Contains(h, "Previous session: old-id.") {
		t.Errorf("stub handoff %q", h)
	}

	// --handoff with a fresh handoff keeps it (no stub) and bumps the generation.
	supervisor.WriteHandoff(f.Home, "open question: deploy web-nav?")
	if gen, err := resetSupervisor(f, true); err != nil || gen != 3 {
		t.Fatalf("second reset gen=%d err=%v", gen, err)
	}
	if h, _, _ := supervisor.ReadHandoff(f.Home); h != "open question: deploy web-nav?" {
		t.Errorf("handoff overwritten: %q", h)
	}
}

func TestSupLine(t *testing.T) {
	now := time.Now()
	s := supervisor.Session{SessionID: "3f2a91c0-aaaa", Generation: 2, Started: now.Add(-(2*time.Hour + 13*time.Minute))}
	if got := supLine(s, 68000, 200000, true, now); got != "admiral 3f2a91c · ctx 34% (68k/200k) · gen 2 · up 2h13m" {
		t.Errorf("%q", got)
	}
	if got := supLine(s, 0, 0, false, now); !strings.Contains(got, "ctx ?") {
		t.Errorf("%q", got)
	}
	s.Profile = "claude-personal"
	if got := supLine(s, 68000, 200000, true, now); got != "admiral 3f2a91c · claude-personal · ctx 34% (68k/200k) · gen 2 · up 2h13m" {
		t.Errorf("with profile: %q", got)
	}
}

func TestSupervisorCmdBootPrompt(t *testing.T) {
	f := testFleet(t)
	t.Setenv("FLEET_ADMIRAL_PROFILE", "copilot")
	sl, err := supervisorCmd(f, "", supervisor.BootPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sl.Cmd, "-i ") || !strings.Contains(sl.Cmd, "fleet_brief") || !strings.Contains(sl.Cmd, "--session-id "+sl.ID) {
		t.Errorf("copilot reset cmd: %s", sl.Cmd)
	}
	if sl, _ := supervisorCmd(f, "", ""); strings.Contains(sl.Cmd, " -i ") {
		t.Errorf("up must not pass a boot prompt: %s", sl.Cmd)
	}
	t.Setenv("FLEET_ADMIRAL_PROFILE", "fake")
	if sl, _ := supervisorCmd(f, "", supervisor.BootPrompt); strings.Contains(sl.Cmd, "fleet_brief") {
		t.Errorf("non-copilot profile got a boot prompt: %s", sl.Cmd)
	}
}

func TestConfirmResetQuotesExe(t *testing.T) {
	f := testFleet(t)
	f.T.Run("new-session", "-d", "-s", "x", "sleep 30")
	if _, err := f.T.Run(append([]string{"bind-key", "-n", "M-R"}, confirmResetArgs("/tmp/my fleet")...)...); err != nil {
		t.Fatal(err)
	}
	out, _ := f.T.Run("list-keys", "-T", "root", "M-R")
	if !strings.Contains(out, `'/tmp/my fleet' admiral reset`) || !strings.Contains(out, "reset admiral? (y/n)") {
		t.Errorf("binding: %s", out)
	}
}

func TestSupervisorAliasWarnsAndDispatches(t *testing.T) {
	f := testFleet(t)
	var buf bytes.Buffer
	old := stderr
	stderr = &buf
	t.Cleanup(func() { stderr = old })
	errNew := cmdAdmiral(f, "admiral", []string{"bogus"})
	if buf.Len() != 0 {
		t.Errorf("admiral warned: %q", buf.String())
	}
	errOld := cmdAdmiral(f, "supervisor", []string{"bogus"})
	if errNew == nil || errOld == nil || errNew.Error() != errOld.Error() || !strings.Contains(errNew.Error(), "fleet admiral") {
		t.Errorf("same handler: %v | %v", errNew, errOld)
	}
	if buf.String() != "fleet: \"supervisor\" is deprecated; use \"fleet admiral\"\n" {
		t.Errorf("warning %q", buf.String())
	}
}

func TestSupervisorAliasResolvesToAdmiral(t *testing.T) {
	if commandName("supervisor") != "admiral" || commandName("admiral") != "admiral" {
		t.Fatalf("supervisor -> %q, admiral -> %q", commandName("supervisor"), commandName("admiral"))
	}
	if commandName("spawn") != "spawn" || commandName("kill") != "kill" {
		t.Fatal("other commands must pass through")
	}
}

func TestAdmiralEnvFallback(t *testing.T) {
	f := testFleet(t)
	t.Setenv("FLEET_ADMIRAL_PROFILE", "")
	t.Setenv("FLEET_ADMIRAL_DIR", "")
	t.Setenv("FLEET_SUPERVISOR_PROFILE", "fake")
	t.Setenv("FLEET_SUPERVISOR_DIR", f.Home)
	sl, err := supervisorCmd(f, "", "")
	if err != nil || sl.Profile != "fake" || sl.Dir != f.Home {
		t.Fatalf("legacy env: %+v %v", sl, err)
	}
	newDir := t.TempDir()
	t.Setenv("FLEET_ADMIRAL_PROFILE", "sup2")
	t.Setenv("FLEET_ADMIRAL_DIR", newDir)
	if sl, err := supervisorCmd(f, "", ""); err != nil || sl.Profile != "sup2" || sl.Dir != newDir {
		t.Fatalf("new env must win: %+v %v", sl, err)
	}
}
