package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/worktree"
)

// wtFleet is a Fleet with a cheap "cat" profile and the given config body.
func wtFleet(t *testing.T, cfgBody string) *Fleet {
	t.Helper()
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte("[cat]\ncommand = \"sh\"\nargs = [\"-c\", \"cat\"]\npatterns = \"fake\"\n"), 0o644)
	cfg := filepath.Join(home, "config.toml")
	os.WriteFile(cfg, []byte(cfgBody), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// A spawn into a git repo runs in the agent's own worktree and branch, and
// the board records both (DESIGN §4, ADR-39).
func TestSpawnWorktree(t *testing.T) {
	f := wtFleet(t, "[defaults]\nplaybook = false\n")
	dir := repo(t)
	a, err := f.Spawn("w1", "cat", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	want := worktree.Path(dir, "w1")
	if a.Dir == dir || filepath.Base(a.Dir) != "w1" {
		t.Fatalf("dir %q want the worktree %q", a.Dir, want)
	}
	if a.Worktree == nil || a.Worktree.Path != a.Dir || a.Worktree.Branch != "fleet/w1" {
		t.Fatalf("board worktree: %+v", a.Worktree)
	}
	if _, err := os.Stat(filepath.Join(a.Dir, "f")); err != nil {
		t.Fatalf("worktree not checked out: %v", err)
	}
	// Close names the branch and path, so work left there isn't lost silently.
	os.WriteFile(filepath.Join(a.Dir, "g"), []byte("mine"), 0o644)
	if note := f.CloseNote("w1"); !strings.Contains(note, a.Worktree.Path) || !strings.Contains(note, "fleet/w1") {
		t.Fatalf("close note: %q", note)
	}
	// The sweep's auto-close check sees the same work.
	if r := AgentBlocker(f.Store.Load().Agents["w1"]); r != "uncommitted changes by the agent: g" {
		t.Fatalf("blocker: %q", r)
	}
	// A second agent in the same repo gets its own worktree, not w1's.
	b, err := f.Spawn("w2", "cat", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if b.Dir == a.Dir || b.Worktree == nil || b.Worktree.Branch != "fleet/w2" {
		t.Fatalf("second agent: %+v", b.Worktree)
	}
}

// A committed but unmerged worktree blocks auto-close and says where it is.
func TestWorktreeBlocksUnmerged(t *testing.T) {
	f := wtFleet(t, "[defaults]\nplaybook = false\n")
	a, err := f.Spawn("w1", "cat", repo(t), "")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(a.Dir, "g"), []byte("mine"), 0o644)
	git(t, a.Dir, "add", "g")
	git(t, a.Dir, "commit", "-q", "-m", "work")
	r := AgentBlocker(f.Store.Load().Agents["w1"])
	if !strings.HasPrefix(r, "worktree left behind (not merged into main)") || !strings.Contains(r, a.Worktree.Path) {
		t.Fatalf("blocker: %q", r)
	}
}

// The opt-out works from config.toml and per spawn, and a queued agent keeps
// the choice the spawn made.
func TestSpawnNoWorktree(t *testing.T) {
	off := false
	f := wtFleet(t, "[defaults]\nplaybook = false\nworktree = false\n")
	dir := repo(t)
	a, err := f.Spawn("w1", "cat", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Dir != dir || a.Worktree != nil {
		t.Fatalf("config opt-out: dir %q worktree %+v", a.Dir, a.Worktree)
	}
	// --worktree / worktree=true overrides the config the other way.
	on := true
	b, err := f.SpawnWith(SpawnOpts{Worktree: &on}, "w2", "cat", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if b.Worktree == nil || b.Worktree.Branch != "fleet/w2" {
		t.Fatalf("per-spawn override: %+v", b.Worktree)
	}
	// Queued with worktrees on, drained after the config turned them off:
	// the entry's own choice wins.
	g := wtFleet(t, "[defaults]\nplaybook = false\nmax_working = 1\n")
	d2 := repo(t)
	if _, err := g.Spawn("q0", "cat", d2, ""); err != nil {
		t.Fatal(err)
	}
	q, err := g.SpawnWith(SpawnOpts{Worktree: &off}, "q1", "cat", d2, "")
	if err != nil || q.Status != "queued" || !q.NoWorktree {
		t.Fatalf("queued: %+v %v", q, err)
	}
	g.startQueued(q)
	if got := g.Store.Load().Agents["q1"]; got.Worktree != nil || got.Dir != d2 {
		t.Fatalf("drained queued agent: dir %q worktree %+v", got.Dir, got.Worktree)
	}
}

// A spawn dir git refuses a worktree for falls back to the dir itself.
func TestSpawnWorktreeFallback(t *testing.T) {
	f := wtFleet(t, "[defaults]\nplaybook = false\n")
	dir := repo(t)
	// Something else already occupies the path the worktree would take.
	os.MkdirAll(worktree.Path(dir, "w1"), 0o755)
	a, err := f.Spawn("w1", "cat", dir, "")
	if err != nil {
		t.Fatalf("spawn must not fail when git refuses: %v", err)
	}
	if a.Dir != dir || a.Worktree != nil {
		t.Fatalf("fallback: dir %q worktree %+v", a.Dir, a.Worktree)
	}
}

// Adopt makes no worktree, but records the one the session was already in.
func TestAdoptRecordsWorktree(t *testing.T) {
	f := wtFleet(t, "[defaults]\nplaybook = false\n")
	dir := repo(t)
	w, err := worktree.Create(dir, "old")
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.start("ad1", "cat", f.Profiles["cat"], w.Path, "", "", "", []string{"sh", "-c", "cat"}, false,
		startExtra{Worktree: adoptWorktree(w.Path), NoWorktree: true})
	if err != nil {
		t.Fatal(err)
	}
	if a.Worktree == nil || a.Worktree.Branch != "fleet/old" || a.Worktree.Path != w.Path {
		t.Fatalf("adopted worktree: %+v", a.Worktree)
	}
	if got := f.Store.Load().Agents["ad1"]; got.Worktree == nil {
		t.Fatal("not on the board")
	}
	// A plain checkout records nothing.
	if adoptWorktree(dir) != nil {
		t.Fatal("main worktree recorded as the agent's own")
	}
}

// Close and fleet_info don't count the agent's own result file as
// uncommitted work (#11).
func TestCloseNoteIgnoresResultFile(t *testing.T) {
	f := wtFleet(t, "[defaults]\nplaybook = false\n")
	a, err := f.Spawn("w1", "cat", repo(t), "")
	if err != nil {
		t.Fatal(err)
	}
	f.Store.Update(func(b *board.Board) error {
		b.Agents["w1"].Card = &board.Card{Result: DefaultResult}
		return nil
	})
	os.WriteFile(filepath.Join(a.Dir, DefaultResult), []byte("done"), 0o644)
	if note := f.CloseNote("w1"); note != "" {
		t.Fatalf("close note for a result file only: %q", note)
	}
	if info, _ := f.Info("w1"); !strings.Contains(info, "git dirty: 0 files") {
		t.Fatalf("info:\n%s", info)
	}
	os.WriteFile(filepath.Join(a.Dir, "g"), []byte("mine"), 0o644)
	if note := f.CloseNote("w1"); !strings.Contains(note, "uncommitted changes") {
		t.Fatalf("close note with other dirt: %q", note)
	}
	if info, _ := f.Info("w1"); !strings.Contains(info, "git dirty: 1 files") {
		t.Fatalf("info:\n%s", info)
	}
}
