package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/wall"
)

type fakeModel struct {
	calls int
	err   error
}

func (f *fakeModel) Summarize(_ context.Context, _ string) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return "building the parser\nwaiting on nothing", nil
}

func summaryServer(t *testing.T) (*Server, *fakeModel) {
	t.Helper()
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte("[yn]\ncommand = \"sh\"\nargs = [\"-c\", \"printf 'Approve? [y/n] '; cat\"]\npatterns = \"fake\"\n"), 0o644)
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	f, err := fleet.New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Spawn("a1", "yn", home, ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if p, _ := f.Peek("a1", 0); strings.Contains(p, "[y/n]") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	m := &fakeModel{}
	f.SummaryModel = m
	return New(f, wall.New(tc, fleet.Session)), m
}

func TestPeekSummaryTool(t *testing.T) {
	s, m := summaryServer(t)
	out, isErr := callText(t, s, "fleet_peek", map[string]any{"name": "a1", "summary": true})
	if isErr {
		t.Fatalf("fleet_peek summary: %s", out)
	}
	if !strings.Contains(out, "building the parser") {
		t.Fatalf("out = %q", out)
	}
	if strings.Contains(out, "[y/n]") {
		t.Fatal("the admiral was given raw pane text, not a summary")
	}
	// Same screen: the cache answers and the model is left alone.
	out, _ = callText(t, s, "fleet_peek", map[string]any{"name": "a1", "summary": true})
	if m.calls != 1 || !strings.Contains(out, "cached") {
		t.Fatalf("calls = %d out = %q", m.calls, out)
	}
	// Without summary=true the tool still returns screen lines.
	out, isErr = callText(t, s, "fleet_peek", map[string]any{"name": "a1"})
	if isErr || !strings.Contains(out, "[y/n]") {
		t.Fatalf("plain peek: %q", out)
	}
}

func TestPeekSummaryFailsOpen(t *testing.T) {
	s, m := summaryServer(t)
	m.err = fmt.Errorf("model unreachable")
	out, isErr := callText(t, s, "fleet_peek", map[string]any{"name": "a1", "summary": true})
	if !isErr || !strings.Contains(out, "fleet_peek without summary") {
		t.Fatalf("want a fail-open error, got %q (isErr %v)", out, isErr)
	}
}

// `summary` is a string for fleet_handoff and a bool for fleet_peek; neither
// may break the other's argument parsing.
func TestSummaryArgumentTakesTextAndBool(t *testing.T) {
	parse := func(raw string, a *args) error { return json.Unmarshal([]byte(raw), a) }
	var a args
	if err := parse(`{"summary":"what the next admiral must know"}`, &a); err != nil {
		t.Fatal(err)
	}
	if a.Summary.Text != "what the next admiral must know" || a.Summary.Flag {
		t.Fatalf("text: %+v", a.Summary)
	}
	a = args{}
	if err := parse(`{"summary":true}`, &a); err != nil {
		t.Fatal(err)
	}
	if !a.Summary.Flag || a.Summary.Text != "" {
		t.Fatalf("bool: %+v", a.Summary)
	}
	a = args{}
	if err := parse(`{"summary":"true"}`, &a); err != nil || !a.Summary.Flag {
		t.Fatalf("string true: %+v err %v", a.Summary, err)
	}
}
