package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/tools"
)

// TestRefreshReadsSubagents: refresh reads the profile's event log for an
// agent with a session id and keeps the offset on the board.
func TestRefreshReadsSubagents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), fmt.Appendf(nil, `[sub]
command = "sh"
args = ["-c", "echo working; cat"]
launch_id = ["{id}"]
subagent_log = "%s/{id}.jsonl"
patterns = "fake"
`, home), 0o644)
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.Spawn("s1", "sub", home, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.SessionID == "" {
		t.Fatal("no session id")
	}
	log, _ := os.ReadFile(filepath.Join("..", "status", "testdata", "copilot-events", "background.jsonl"))
	os.WriteFile(filepath.Join(home, a.SessionID+".jsonl"), log, 0o644)
	as, err := f.Refresh()
	if err != nil || len(as) != 1 {
		t.Fatal(as, err)
	}
	// Only builder #2 still runs; the finished ones are long past the
	// 5-minute window by the wall clock, but their agentIds are remembered.
	if n := status.RunningCount(as[0].Subagents); n != 1 || as[0].SubLogOffset != int64(len(log)) || len(as[0].SubKnown) != 3 {
		t.Errorf("running %d, offset %d of %d: %+v", n, as[0].SubLogOffset, len(log), as[0].Subagents)
	}
	if got := f.Store.Load().Agents["s1"]; status.RunningCount(got.Subagents) != 1 || got.SubLogOffset != int64(len(log)) || len(got.SubKnown) != 3 {
		t.Errorf("board not saved: %+v", got)
	}
}

func TestSubagentsGoneOrNoLog(t *testing.T) {
	now := time.Now()
	f := &Fleet{Profiles: map[string]tools.Profile{"p": {}}}
	a := &board.Agent{Profile: "p", SessionID: "x", Subagents: []status.Subagent{
		{ID: "1", State: status.SubRunning, Started: now.Add(-time.Minute)},
		{ID: "2", State: status.SubDone, Started: now.Add(-time.Hour), Ended: now.Add(-time.Hour)},
	}}
	f.subagents(a, true, now) // profile without a log: only pruned
	if len(a.Subagents) != 1 || a.Subagents[0].State != status.SubRunning {
		t.Errorf("no log: %+v", a.Subagents)
	}
	f.subagents(a, false, now) // pane gone/dead: running -> failed
	if len(a.Subagents) != 1 || a.Subagents[0].State != status.SubFailed || !a.Subagents[0].Ended.Equal(now) {
		t.Errorf("gone: %+v", a.Subagents)
	}
}

// TestRefreshListPanesErrorKeepsSubagents: when tmux can't list panes (here no
// server on the socket) running subagents are left alone, not failed.
func TestRefreshListPanesErrorKeepsSubagents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.T.ListPanes(""); err == nil {
		t.Skip("socket unexpectedly has a server")
	}
	run := []status.Subagent{{ID: "1", Name: "x", State: status.SubRunning, Started: time.Now()}}
	f.Store.Update(func(b *board.Board) error {
		b.Agents["a"] = &board.Agent{Name: "a", Profile: "copilot", SessionID: "s", Subagents: run}
		return nil
	})
	as, _ := f.Refresh()
	if len(as) != 1 || status.RunningCount(as[0].Subagents) != 1 {
		t.Errorf("subagents changed on a tmux error: %+v", as)
	}
}
