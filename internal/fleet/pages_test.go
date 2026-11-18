package fleet

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/pages"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// An answer no agent took lands in the admiral's inbox as a question that
// carries the picks and how to take them (ADR-163).
func TestPageAnswerReachesAdmiral(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "none.toml"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	f.handPageAnswers([]pages.Answer{{Page: "decide-x", Decision: "decide-x-1", Line: "decide-x: style A", T: float64(time.Now().Unix())}})
	msgs := f.Mail().Peek(mail.Admiral)
	if len(msgs) != 1 || msgs[0].From != "decide-x" {
		t.Fatalf("admiral inbox = %+v, want one report from decide-x", msgs)
	}
	for _, want := range []string{"question: answer from page decide-x", `"decide-x: style A"`, "artifact answer decide-x"} {
		if !strings.Contains(msgs[0].Text, want) {
			t.Errorf("report %q lacks %q", msgs[0].Text, want)
		}
	}
}
