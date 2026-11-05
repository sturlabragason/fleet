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
	"github.com/sturlabragason/fleet/internal/tools"
)

// spendFleet puts two agents on a board: one copilot-style session with a
// captured usage log, one profile that records nothing.
func spendFleet(t *testing.T) *Fleet {
	t.Helper()
	home := t.TempDir()
	log, err := os.ReadFile(filepath.Join("..", "spend", "testdata", "copilot-events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "SID.jsonl"), log, 0o644); err != nil {
		t.Fatal(err)
	}
	// A socket with no server: pane lookups fail and Info leaves "mode" empty.
	f := &Fleet{
		T:     &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"},
		Home:  home,
		Store: board.NewStore(home),
		Profiles: map[string]tools.Profile{
			"cop":  {Patterns: "copilot", SubagentLog: filepath.Join(home, "{id}.jsonl")},
			"mute": {Patterns: "agy"},
		},
	}
	err = f.Store.Update(func(b *board.Board) error {
		b.Agents = map[string]*board.Agent{
			"two": {Name: "two", Profile: "mute", SessionID: "OTHER"},
			"one": {Name: "one", Profile: "cop", SessionID: "SID"},
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSpendAll(t *testing.T) {
	f := spendFleet(t)
	rows, total := f.SpendAll()
	if len(rows) != 2 || rows[0].Name != "one" || rows[1].Name != "two" {
		t.Fatalf("rows %+v", rows)
	}
	if !rows[0].Known || rows[0].Calls != 3 || rows[0].Short() != "12.8 AIC" {
		t.Fatalf("agent one: %+v", rows[0])
	}
	if rows[1].Known || rows[1].Calls != 0 {
		t.Fatalf("a profile with no usage records must stay unknown: %+v", rows[1])
	}
	if total.Calls != 3 || total.Short() != "12.8 AIC" {
		t.Fatalf("total %+v", total)
	}

	if s, ok := f.Spend("one"); !ok || s.Calls != 3 {
		t.Fatalf("Spend(one) = %+v %v", s, ok)
	}
	if _, ok := f.Spend("nobody"); ok {
		t.Fatal("unknown agent reported spend")
	}
}

// Info carries the spend line only for a tool that records usage.
func TestInfoSpendLine(t *testing.T) {
	f := spendFleet(t)
	got, err := f.Info("one")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "spend: 12.8 AIC (3 calls, 61k tokens, 55k cached)") {
		t.Fatalf("info: %q", got)
	}
	if got, err = f.Info("two"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "spend:") {
		t.Fatalf("mute profile got a spend line: %q", got)
	}
}
