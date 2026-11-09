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
	"github.com/sturlabragason/fleet/internal/tmux"
)

// queueFleet is a Fleet on its own fleet-test-* socket with a cheap "cat"
// profile and max_working from cfg.
func queueFleet(t *testing.T, maxWorking int) *Fleet {
	t.Helper()
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte("[cat]\ncommand = \"sh\"\nargs = [\"-c\", \"cat\"]\npatterns = \"fake\"\n"), 0o644)
	cfg := filepath.Join(home, "config.toml")
	os.WriteFile(cfg, []byte(fmt.Sprintf("[defaults]\nmax_working = %d\n", maxWorking)), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	// The memory guard must never read this machine's RAM in a test.
	f.FreeMem = func() (uint64, error) { return 64 << 30, nil }
	if f.Prefs.MaxWorking() != maxWorking {
		t.Fatalf("max_working %d", f.Prefs.MaxWorking())
	}
	return f
}

func TestSpawnQueuesAtCapAndDrains(t *testing.T) {
	f := queueFleet(t, 1)
	if a, err := f.Spawn("a1", "cat", "", ""); err != nil || a.Status != "unknown" {
		t.Fatalf("a1: %v %+v", err, a)
	}
	a2, err := f.Spawn("a2", "cat", "", "do the thing")
	if err != nil {
		t.Fatal(err)
	}
	if a2.Status != "queued" || a2.PaneID != "" || a2.Goal != "do the thing" {
		t.Fatalf("a2 not queued: %+v", a2)
	}
	if f.paneOf("a2") != "" {
		t.Fatal("queued agent has a pane")
	}
	if _, err := f.Spawn("a2", "cat", "", ""); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("duplicate queued name: %v", err)
	}
	queuedAt := f.Store.Load().Agents["a2"].Created
	// Without DrainQueue a queued agent stays queued, never gone.
	f.Refresh()
	if st := f.Store.Load().Agents["a2"].Status; st != "queued" {
		t.Fatalf("a2 after refresh: %s", st)
	}
	// Still busy: draining must not start it.
	f.DrainQueue = true
	f.Refresh()
	if st := f.Store.Load().Agents["a2"].Status; st != "queued" {
		t.Fatalf("a2 started while a1 busy: %s", st)
	}
	// a1 goes idle (closing it would end the test's only tmux session, and
	// a failed pane listing never drains). Its pane is unchanged, so
	// Refresh keeps idle.
	f.Store.Update(func(b *board.Board) error { b.Agents["a1"].Status = "idle"; return nil })
	f.Refresh()
	if st := f.Store.Load().Agents["a2"].Status; st != "starting" {
		t.Fatalf("a2 not claimed: %s", st)
	}
	var pane string
	for i := 0; i < 30 && pane == ""; i++ {
		time.Sleep(100 * time.Millisecond)
		pane = f.paneOf("a2")
	}
	if pane == "" {
		t.Fatal("a2 never got a pane")
	}
	time.Sleep(100 * time.Millisecond)
	a := f.Store.Load().Agents["a2"]
	if a.PaneID != pane || !a.Created.Equal(queuedAt) || a.Goal != "do the thing" {
		t.Errorf("started a2: %+v (queued at %v)", a, queuedAt)
	}
}

func TestMaxWorkingZeroNeverQueues(t *testing.T) {
	f := queueFleet(t, 0)
	for i := 0; i < 3; i++ {
		a, err := f.Spawn(fmt.Sprintf("z%d", i), "cat", "", "")
		if err != nil || a.Status == "queued" || a.PaneID == "" {
			t.Fatalf("z%d: %v %+v", i, err, a)
		}
	}
}

func TestQueuedSendFailsAndCloseRemoves(t *testing.T) {
	f := queueFleet(t, 1)
	if _, err := f.Spawn("a1", "cat", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Spawn("q1", "cat", "", ""); err != nil {
		t.Fatal(err)
	}
	err := f.Send("q1", "hi")
	if err == nil || err.Error() != `agent "q1" is queued, not started yet` {
		t.Errorf("send to queued: %v", err)
	}
	// The MCP sends hold text for it instead: appended to the first prompt
	// the drain launches with. A started agent is not held for.
	if held, err := f.HoldForQueued("q1", "hi"); !held || err != nil {
		t.Fatalf("hold: %v %v", held, err)
	}
	f.HoldForQueued("q1", "and this")
	if g := f.Store.Load().Agents["q1"].Goal; g != "hi\n\nand this" {
		t.Errorf("held goal %q", g)
	}
	if held, _ := f.HoldForQueued("a1", "x"); held {
		t.Error("held for a running agent")
	}
	if err := f.Close("q1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Store.Load().Agents["q1"]; ok {
		t.Error("close left queued agent on the board")
	}
}

func TestQueueOrderAndStartTimeout(t *testing.T) {
	t0 := time.Now()
	as := []*board.Agent{
		{Name: "c", Status: "queued", Created: t0.Add(time.Second)},
		{Name: "b", Status: "queued", Created: t0},
		{Name: "a", Status: "queued", Created: t0},
		{Name: "w", Status: "working", Created: t0.Add(-time.Hour)},
	}
	var got []string
	for _, a := range QueueOrder(as) {
		got = append(got, a.Name)
	}
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("order %v", got)
	}
	s := &board.Agent{Status: "starting", StatusSince: t0}
	if keepPending(s, t0.Add(time.Minute)) != "starting" {
		t.Error("starting timed out early")
	}
	if keepPending(s, t0.Add(3*time.Minute)) != "error" || s.LastLine != "queue start timed out" {
		t.Errorf("timeout: %+v", s)
	}
	f := &Fleet{OffWallEvery: 5 * time.Second}
	q := &board.Agent{Name: "q", Status: "queued"}
	f.skipCapture(q, true, false, t0)
	if f.skipCapture(q, true, false, t0.Add(time.Second)) {
		t.Error("skipCapture skipped a queued agent")
	}
}

func TestQueueIsFIFOWhenSlotFrees(t *testing.T) {
	f := queueFleet(t, 1)
	f.Spawn("a1", "cat", "", "")
	f.Spawn("q1", "cat", "", "")
	f.Close("a1") // slot free, but q1 waits for the next poll
	if a, err := f.Spawn("n1", "cat", "", ""); err != nil || a.Status != "queued" {
		t.Fatalf("new spawn jumped the queue: %v %+v", err, a)
	}
}

func TestNoDrainWhenPaneListingFails(t *testing.T) {
	f := queueFleet(t, 1) // no tmux server on this socket yet: ListPanes fails
	now := time.Now()
	f.Store.Update(func(b *board.Board) error {
		b.Agents["q1"] = &board.Agent{Name: "q1", Profile: "cat", Status: "queued", Created: now, StatusSince: now}
		return nil
	})
	if _, err := f.T.ListPanes(""); err == nil {
		t.Skip("listing did not fail")
	}
	f.DrainQueue = true
	f.Refresh()
	if st := f.Store.Load().Agents["q1"].Status; st != "queued" {
		t.Errorf("drained on a failed listing: %s", st)
	}
}

func TestClosedWhileStartingKillsPane(t *testing.T) {
	f := queueFleet(t, 1)
	// No board entry in "starting": as if fleet close ran mid-launch.
	if _, err := f.launch("gone1", "cat", t.TempDir(), "", true, false, nil, Role{}, ""); err == nil {
		t.Fatal("launch of a closed claim succeeded")
	}
	if p := f.paneOf("gone1"); p != "" {
		t.Errorf("pane %s left running", p)
	}
	if _, ok := f.Store.Load().Agents["gone1"]; ok {
		t.Error("closed agent resurrected on the board")
	}
}

func TestRefreshDuringQueuedStartKeepsPane(t *testing.T) {
	f := queueFleet(t, 1)
	now := time.Now()
	if err := f.Store.Update(func(b *board.Board) error {
		b.Agents["q1"] = &board.Agent{Name: "q1", Profile: "cat", Status: "starting", Created: now, StatusSince: now}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	afterTag = func(f *Fleet, pane string) { f.Refresh() }
	defer func() { afterTag = nil }()
	if _, err := f.launch("q1", "cat", t.TempDir(), "", true, false, nil, Role{}, ""); err != nil {
		t.Fatalf("launch after a racing Refresh: %v", err)
	}
	if f.paneOf("q1") == "" {
		t.Error("pane killed after a racing Refresh adopted it")
	}
}

func TestUnknownBusyOnlyWhileFresh(t *testing.T) {
	now := time.Now()
	cases := []struct {
		a    board.Agent
		want bool
	}{
		{board.Agent{Status: "unknown", StatusSince: now.Add(-time.Minute)}, true},
		{board.Agent{Status: "unknown", StatusSince: now.Add(-3 * time.Minute)}, false},
		{board.Agent{Status: "unknown", Created: now.Add(-3 * time.Minute)}, false},
		{board.Agent{Status: "stuck", StatusSince: now.Add(-time.Hour)}, true},
		{board.Agent{Status: "queued"}, false},
		{board.Agent{Status: "idle"}, false},
	}
	for _, c := range cases {
		if got := busy(&c.a, now); got != c.want {
			t.Errorf("%+v: busy %v", c.a, got)
		}
	}
}
