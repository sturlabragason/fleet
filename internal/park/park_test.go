package park

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
)

func ag(name, st string, idleFor time.Duration, session string) *board.Agent {
	return &board.Agent{Name: name, Status: st, StatusSince: time.Now().Add(-idleFor), SessionID: session}
}

func TestCandidatesLongestIdleFirst(t *testing.T) {
	as := []*board.Agent{
		ag("new", "idle", time.Minute, "s1"),
		ag("old", "idle", time.Hour, "s2"),
		ag("middle", "idle", 10*time.Minute, "s3"),
	}
	got := Candidates(as, nil)
	want := []string{"old", "middle", "new"}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates, want %d", len(got), len(want))
	}
	for i, n := range want {
		if got[i].Name != n {
			t.Errorf("candidate %d = %s, want %s", i, got[i].Name, n)
		}
	}
}

func TestCandidatesProtectWorkingWaitingPinnedAndSessionless(t *testing.T) {
	as := []*board.Agent{
		ag("working", "working", 2*time.Hour, "s"),
		ag("waiting", "waiting", 2*time.Hour, "s"),
		ag("stuck", "stuck", 2*time.Hour, "s"),
		ag("starting", "starting", 2*time.Hour, "s"),
		ag("queued", "queued", 2*time.Hour, ""),
		ag("parked", "parked", 2*time.Hour, "s"),
		ag("gone", "gone", 2*time.Hour, "s"),
		ag("error", "error", 2*time.Hour, "s"),
		ag("unknown", "unknown", 2*time.Hour, "s"),
		ag("pinned", "idle", 3*time.Hour, "s"),
		ag("sessionless", "idle", 4*time.Hour, ""),
		ag("ok", "idle", time.Minute, "s"),
	}
	got := Candidates(as, map[string]bool{"pinned": true})
	if len(got) != 1 || got[0].Name != "ok" {
		var names []string
		for _, a := range got {
			names = append(names, a.Name)
		}
		t.Fatalf("candidates %v, want [ok]", names)
	}
	if Blocker(as[0], nil) == "" || Blocker(as[9], map[string]bool{"pinned": true}) == "" || Blocker(nil, nil) == "" {
		t.Error("Blocker must give a reason for a working, pinned or missing agent")
	}
	if why := Blocker(as[len(as)-1], nil); why != "" {
		t.Errorf("idle agent blocked: %s", why)
	}
}

func TestFreeReadsMemAvailable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "meminfo")
	os.WriteFile(p, []byte("MemTotal:       32000000 kB\nMemFree:          100000 kB\nMemAvailable:    4194304 kB\n"), 0o644)
	old := MemInfo
	MemInfo = p
	defer func() { MemInfo = old }()
	n, err := Free()
	if err != nil || n != 4<<30 {
		t.Fatalf("Free() = %d, %v; want %d", n, err, uint64(4<<30))
	}
	os.WriteFile(p, []byte("MemTotal: 1 kB\n"), 0o644)
	if _, err := Free(); err == nil {
		t.Error("a file without MemAvailable must be an error, not 0 free")
	}
}

func TestBytes(t *testing.T) {
	for _, c := range []struct {
		n    uint64
		want string
	}{{512, "512 B"}, {4 << 30, "4.0 GiB"}, {3*(1<<30) + (1 << 29), "3.5 GiB"}, {2 << 20, "2.0 MiB"}} {
		if got := Bytes(c.n); got != c.want {
			t.Errorf("Bytes(%d) = %s, want %s", c.n, got, c.want)
		}
	}
}
