package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/tools"
)

// A prompt typed in the pane marks the agent as the user's; fleet's own
// prompts don't.
func TestTranscriptPaneEntryIsUser(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(acphost.Dir(home), 0o700)
	at := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	writeTranscript(t, home, "a1",
		acphost.Entry{At: at, Kind: acphost.KindUser, Text: "mine", By: acphost.ByPane},
		acphost.Entry{At: at.Add(time.Second), Kind: acphost.KindUser, Text: "fleet's"})
	if tr := readTranscript(acphost.TranscriptPath(home, "a1"), false); !tr.UserAt.Equal(at) {
		t.Errorf("user_at %v want %v", tr.UserAt, at)
	}
}

// Takeover parks an acp agent and resumes its session in the tool's own
// screen on tmux, marks it as the user's, and hands it back to an ACP host
// on the same session (ADR-82).
func TestTakeoverMovesSessionBetweenDrivers(t *testing.T) {
	home := t.TempDir()
	noPlaybook(t, home)
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "none.toml"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	host := filepath.Join(home, "fakehost")
	os.WriteFile(host, []byte("#!/bin/sh\nexec sleep 600\n"), 0o755)
	tool := filepath.Join(home, "stubtool")
	os.WriteFile(tool, []byte("#!/bin/sh\nexec sleep 600\n"), 0o755)
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(fmt.Sprintf(`[stub]
command = %q
acp = ["--acp"]
resume = ["--resume", "{id}"]
[noresume]
command = %q
acp = ["--acp"]
`, tool, tool)), 0o644)
	f, err := New(tc, home, host)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	wt := false
	if _, err := f.SpawnWith(SpawnOpts{Driver: "acp", Worktree: &wt}, "a1", "stub", dir, ""); err != nil {
		t.Fatal(err)
	}
	stub := &stubHost{exitAfter: 10 * time.Millisecond}
	stub.serve(t, home, "a1")
	writeTranscript(t, home, "a1", acphost.Entry{Kind: acphost.KindSession, SessionID: "S1"})
	if _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}

	if _, err := f.Takeover("a1", true); err == nil || !strings.Contains(err.Error(), "already runs on the acp driver") {
		t.Errorf("takeover to acp of an acp agent: %v", err)
	}
	// A working agent is refused and keeps running on acp.
	f.Store.Update(func(b *board.Board) error { b.Agents["a1"].Status = "working"; return nil })
	if _, err := f.Takeover("a1", false); err == nil || !strings.Contains(err.Error(), "can't take over") {
		t.Errorf("takeover of a working agent: %v", err)
	}
	if a := f.Store.Load().Agents["a1"]; !a.ACP() || f.paneOf("a1") == "" {
		t.Fatalf("refused takeover changed the agent: driver %q pane %q", a.Driver, f.paneOf("a1"))
	}
	f.Store.Update(func(b *board.Board) error { b.Agents["a1"].Status = "idle"; return nil })

	before := time.Now()
	a, err := f.Takeover("a1", false)
	if err != nil {
		t.Fatal(err)
	}
	if a.ACP() || a.ACPServer != "" || a.SessionID != "S1" || a.UserAt.Before(before) {
		t.Errorf("after takeover: driver %q server %q session %q user_at %v", a.Driver, a.ACPServer, a.SessionID, a.UserAt)
	}
	if got, want := startCommand(t, tc, a.PaneID), tools.ShellCommand([]string{tool, "--resume", "S1"}); got != want {
		t.Errorf("tmux window command:\n%s\nwant\n%s", got, want)
	}
	if f.SendBlocker("a1", true) == "" {
		t.Error("the admiral may send over an agent the user just took over")
	}

	// And back, once the tool's screen reads idle: a new host on the same session.
	f.Store.Update(func(b *board.Board) error { b.Agents["a1"].Status = "idle"; return nil })
	b, err := f.Takeover("a1", true)
	if err != nil {
		t.Fatal(err)
	}
	cmd := startCommand(t, tc, b.PaneID)
	if !b.ACP() || !strings.Contains(cmd, "acp-host a1") || !strings.Contains(cmd, "--session S1") {
		t.Errorf("back to acp: driver %q command %s", b.Driver, cmd)
	}

	// A profile that can't resume in its own screen is refused up front.
	if _, err := f.SpawnWith(SpawnOpts{Driver: "acp", Worktree: &wt}, "a2", "noresume", dir, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Takeover("a2", false); err == nil || !strings.Contains(err.Error(), "can't resume") {
		t.Errorf("takeover without resume args: %v", err)
	}
	if a := f.Store.Load().Agents["a2"]; !a.ACP() || a.Status == "parked" {
		t.Errorf("refused takeover parked it: %+v", a)
	}
}
