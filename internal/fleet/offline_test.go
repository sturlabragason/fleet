package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// TestNetworkOutage runs an outage end to end (ADR-110): fake ACP agents
// whose transport fails while a file exists, and a probe reading the same
// file. During the outage a failed turn shows offline, a host that dies is
// held (no crash report, no close), a new spawn queues and the admiral is
// told once; when the network is back the failed agent gets a continue,
// the dead host is restarted on its session, the spawn starts, and the
// admiral is told once more.
func TestNetworkOutage(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the fleet binary")
	}
	home := t.TempDir()
	noPlaybook(t, home)
	t.Setenv("FLEET_HOME", home)
	t.Setenv("FAKE_ACP_STATE", filepath.Join(home, "fake-state"))
	down := filepath.Join(home, "netdown")
	t.Setenv("FAKE_ACP_NET", down)
	exe := buildFleet(t)
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := New(tc, home, exe)
	if err != nil {
		t.Fatal(err)
	}
	f.Housekeeping, f.DrainQueue, f.DrainSync, f.NetSync = true, true, true, true
	f.NetProbe = func() bool { _, err := os.Stat(down); return err != nil }
	for _, v := range []*time.Duration{&NetEvery, &NetOfflineEvery, &NetTellAfter} {
		old := *v
		*v = 0
		t.Cleanup(func() { *v = old })
	}
	dir := t.TempDir()
	wt := false
	spawn := func(name, prompt string) {
		t.Helper()
		if _, err := f.SpawnWith(SpawnOpts{Driver: "acp", Worktree: &wt}, name, "fake", dir, prompt); err != nil {
			t.Fatal(err)
		}
	}
	spawn("a1", "first [slow 100]")
	spawn("b1", "first [slow 100]")
	waitStatus(t, f, "a1", "idle", 20*time.Second)
	waitStatus(t, f, "b1", "idle", 20*time.Second)
	if err := f.Park("b1", ""); err != nil {
		t.Fatal(err)
	}

	// The network goes.
	if err := os.WriteFile(down, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Send("a1", "second"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, f, "a1", "offline", 20*time.Second)
	if off, _ := f.Offline(); !off {
		t.Fatal("not offline")
	}
	// A wake in the outage: session/load fails and the host dies.
	if _, err := f.Wake("b1", true); err != nil {
		t.Fatal(err)
	}
	spawn("c1", "later [slow 100]")
	for i := 0; i < 30; i++ {
		if _, err := f.Refresh(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	b := f.Store.Load()
	if b1 := b.Agents["b1"]; b1 == nil || b1.Exit != nil || b1.Status != "offline" || !f.net.held["b1"] {
		t.Fatalf("b1 reaped in the outage: %+v", b1)
	}
	if c1 := b.Agents["c1"]; c1 == nil || c1.Status != "queued" || !strings.Contains(c1.LastLine, "network") {
		t.Fatalf("c1 started in the outage: %+v", c1)
	}
	if a1 := b.Agents["a1"]; a1.Status != "offline" {
		t.Fatalf("a1 %s in the outage", a1.Status)
	}
	reports := func() string {
		ms, _ := f.Mail().Inbox(mail.Admiral, true, 50)
		var s []string
		for _, m := range ms {
			s = append(s, m.From+": "+m.Text)
		}
		return strings.Join(s, "\n")
	}
	got := reports()
	if strings.Contains(got, "crashed") || strings.Count(got, "network down") != 1 {
		t.Fatalf("admiral reports in the outage:\n%s", got)
	}

	// The network is back.
	os.Remove(down)
	waitStatus(t, f, "a1", "idle", 20*time.Second)
	if p, _ := f.Peek("a1", 0); !strings.Contains(p, ResumeText) {
		t.Errorf("a1 not resumed:\n%s", p)
	}
	waitStatus(t, f, "b1", "idle", 20*time.Second)
	waitStatus(t, f, "c1", "idle", 20*time.Second)
	if off, _ := f.Offline(); off {
		t.Error("still offline")
	}
	got = reports()
	if strings.Contains(got, "crashed") || strings.Count(got, "network back") != 1 || !strings.Contains(got, "a1") || !strings.Contains(got, "b1 (restarted)") {
		t.Fatalf("admiral reports after the outage:\n%s", got)
	}
}

// An offline file no prober rewrites stops counting after NetStale, so a
// watcher that died in an outage does not hold the fleet forever.
func TestOfflineStale(t *testing.T) {
	f := &Fleet{Home: t.TempDir()}
	f.writeOffline(offlineFile{Since: time.Now()})
	if off, _ := f.Offline(); !off {
		t.Fatal("fresh file not offline")
	}
	old := time.Now().Add(-NetStale - time.Minute)
	os.Chtimes(f.offlinePath(), old, old)
	if off, _ := f.Offline(); off {
		t.Fatal("stale file still offline")
	}
}
