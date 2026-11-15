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
	"github.com/sturlabragason/fleet/internal/tmux"
)

// An ask open past [reports] resurface_after wakes its owner once, across
// passes; resurface_after = "0" never does (ADR-130).
func TestResurfaceWakesOwnerOncePerAsk(t *testing.T) {
	for _, tc := range []struct {
		after string
		want  int
	}{{"2h", 1}, {"0", 0}} {
		t.Run(tc.after, func(t *testing.T) {
			home := t.TempDir()
			cfg := filepath.Join(home, "config.toml")
			os.WriteFile(cfg, []byte("[reports]\nresurface_after = \""+tc.after+"\"\n"), 0o644)
			t.Setenv("FLEET_CONFIG", cfg)
			tc2 := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
			t.Cleanup(func() { tc2.KillServer() })
			f, err := New(tc2, home, "/nonexistent")
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			f.Store.Update(func(b *board.Board) error {
				b.Agents["asker"] = &board.Agent{Name: "asker", Status: "idle", Report: &board.Report{State: "question", Summary: "which palette?", At: now.Add(-3 * time.Hour)}}
				b.Agents["fresh"] = &board.Agent{Name: "fresh", Status: "idle", Report: &board.Report{State: "question", Summary: "too new", At: now.Add(-time.Hour)}}
				b.Agents["moved"] = &board.Agent{Name: "moved", Status: "working", TurnAt: now.Add(-time.Hour), Report: &board.Report{State: "blocked", Summary: "answered", At: now.Add(-5 * time.Hour)}}
				return nil
			})
			pass := func() {
				var as []*board.Agent
				for _, a := range f.Store.Load().Agents {
					as = append(as, a)
				}
				f.resurface(as, now)
			}
			pass()
			pass()
			var got []string
			for _, m := range f.Mail().Peek(mail.Supervisor) {
				if strings.HasPrefix(m.Text, "question: resurface: ") {
					got = append(got, m.Text)
				}
			}
			if len(got) != tc.want {
				t.Fatalf("want %d re-surface notes, got %q", tc.want, got)
			}
			if tc.want == 1 && got[0] != "question: resurface: asker has waited 3h on the user: which palette?" {
				t.Errorf("note %q", got[0])
			}
		})
	}
}
