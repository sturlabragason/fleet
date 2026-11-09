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
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/spend"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/tools"
)

// An agent past recycle_compactions gets one hint to its owner, never a
// second; one below every threshold gets none (ADR-71).
func TestRecycleTellsOwnerOnce(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "config.toml")
	os.WriteFile(cfg, []byte("[defaults]\nrecycle_compactions = 2\nrecycle_aic = 0\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Join(home, "sessions")
	src, err := os.ReadFile("../spend/testdata/copilot-compaction.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for id, body := range map[string][]byte{"big": src, "small": src[:strings.Index(string(src), "session.compaction_complete")]} {
		os.MkdirAll(filepath.Join(sessions, id), 0o755)
		os.WriteFile(filepath.Join(sessions, id, "events.jsonl"), body, 0o644)
	}
	f.Profiles["cp"] = tools.Profile{Name: "cp", Patterns: "copilot", SubagentLog: filepath.Join(sessions, "{id}", "events.jsonl")}
	f.Store.Update(func(b *board.Board) error {
		b.Agents["a1"] = &board.Agent{Name: "a1", Profile: "cp", SessionID: "big"}
		b.Agents["a2"] = &board.Agent{Name: "a2", Profile: "cp", SessionID: "small"}
		return nil
	})
	hints := func() (n int, text string) {
		for _, m := range f.Mail().Peek(mail.Supervisor) {
			if strings.Contains(m.Text, "recycle:") {
				n++
				text = m.Text
			}
		}
		return n, text
	}
	scan := func() {
		var as []board.Agent
		for _, a := range f.Store.Load().Agents {
			if a.RecycleTold.IsZero() {
				as = append(as, *a)
			}
		}
		f.recycleScan(as, time.Now())
	}
	scan()
	n, text := hints()
	if n != 1 || !strings.Contains(text, "a1 has compacted 2 times") || !strings.Contains(text, "result.md") || strings.Contains(text, "⟲") {
		t.Fatalf("hints %d: %q", n, text)
	}
	if f.Store.Load().Agents["a1"].RecycleTold.IsZero() {
		t.Fatal("a1 not marked told")
	}
	b, _ := os.ReadFile(f.wakePath(mail.Supervisor))
	if !strings.Contains(string(b), "hand over to a fresh agent") {
		t.Errorf("wake line: %q", b)
	}
	scan()
	if n, _ := hints(); n != 1 {
		t.Fatalf("told %d times, want once", n)
	}
}

func TestRecycleLine(t *testing.T) {
	got := RecycleLine("w1", spend.Spend{AIC: 1200, HasAIC: true})
	if !strings.HasPrefix(got, "[fleet] w1 has spent 1200 AIC: hand over") {
		t.Fatal(got)
	}
}
