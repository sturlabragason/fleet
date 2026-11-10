package fleet

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/summary"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/tools"
)

type fakeModel struct {
	prompt string
	calls  int
	err    error
}

func (f *fakeModel) Summarize(_ context.Context, prompt string) (string, error) {
	f.calls++
	f.prompt = prompt
	if f.err != nil {
		return "", f.err
	}
	return "waiting on a y/n prompt\nnothing has failed", nil
}

// summaryFleet spawns one fake agent whose pane prints a prompt.
func summaryFleet(t *testing.T) (*Fleet, *fakeModel) {
	t.Helper()
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte("[yn]\ncommand = \"sh\"\nargs = [\"-c\", \"printf 'Approve? [y/n] '; cat\"]\npatterns = \"fake\"\n"), 0o644)
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	t.Setenv("FLEET_HOME", home)
	f, err := New(tc, home, "/nonexistent")
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
	return f, m
}

func TestPeekSummary(t *testing.T) {
	f, m := summaryFleet(t)
	r, err := f.PeekSummary(context.Background(), "a1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "waiting on a y/n prompt\nnothing has failed" || r.Cached {
		t.Fatalf("r = %+v", r)
	}
	if !strings.Contains(m.prompt, "[y/n]") {
		t.Fatalf("the pane never reached the model:\n%s", m.prompt)
	}
	// An unchanged pane is answered from ~/.fleet/peek-cache, not the model.
	again, err := f.PeekSummary(context.Background(), "a1")
	if err != nil || !again.Cached || m.calls != 1 {
		t.Fatalf("again = %+v err = %v calls = %d", again, err, m.calls)
	}
	if _, err := os.Stat(filepath.Join(f.Home, "peek-cache", "a1.txt")); err != nil {
		t.Fatalf("no cache file: %v", err)
	}
	if _, err := f.PeekSummary(context.Background(), "nobody"); err == nil {
		t.Fatal("want an error for an unknown agent")
	}
}

func TestPeekSummaryOffAndFailOpen(t *testing.T) {
	f, m := summaryFleet(t)
	m.err = fmt.Errorf("model unreachable")
	if _, err := f.PeekSummary(context.Background(), "a1"); err == nil {
		t.Fatal("want an error, not a hang, when the model is down")
	}

	// [summary] enabled = false and no injected model: no call at all.
	cfg := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfg, []byte("[summary]\nenabled = false\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	f2, err := New(f.T, f.Home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f2.Summarizer(); err == nil {
		t.Fatal("want summaries to be off")
	}
}

func TestSummarizerUsesTheConfiguredModel(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "config.toml")
	os.WriteFile(cfg, []byte("[summary]\nmodel = \"claude-haiku-5.5\"\ncommand = \"echo\"\nmin_interval = \"5s\"\ntimeout = \"9s\"\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	f, err := New(&tmux.Client{Socket: "fleet-test-unused", Config: "/dev/null"}, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	s, err := f.Summarizer()
	if err != nil {
		t.Fatal(err)
	}
	cli, ok := s.Model.(*summary.CLI)
	if !ok {
		t.Fatalf("model = %T", s.Model)
	}
	if cli.Model != "claude-haiku-5.5" || cli.Command != "echo" || cli.Timeout != 9*time.Second {
		t.Fatalf("cli = %+v", cli)
	}
	if s.MinInterval != 5*time.Second {
		t.Fatalf("min interval = %s", s.MinInterval)
	}
}

// last_message (#16): the screen when the tool keeps no log, the Claude
// session .jsonl for claude patterns, fleet's own transcript for ACP.
func TestLastMessageSources(t *testing.T) {
	f, m := summaryFleet(t)
	got, err := f.LastMessage("a1")
	if err != nil || !strings.HasPrefix(got, LastMessageScreenNote+"\n") || !strings.Contains(got, "[y/n]") {
		t.Fatalf("screen fallback: %q %v", got, err)
	}
	if _, err := f.PeekSummary(context.Background(), "a1"); err != nil || strings.Contains(m.prompt, LastMessageScreenNote) {
		t.Fatalf("summary took the screen fallback as a last message (%v):\n%s", err, m.prompt)
	}
	if _, err := f.LastMessage("nobody"); err == nil {
		t.Fatal("want an error for an unknown agent")
	}

	fixture, err := os.ReadFile(filepath.Join("..", "status", "testdata", "claude-session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.Home, "projects", "-work")
	must(t, os.MkdirAll(dir, 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "s16.jsonl"), fixture, 0o644))
	f.Profiles["cl"] = tools.Profile{Patterns: "claude", SessionPath: filepath.Join(f.Home, "projects", "*", "{id}.jsonl")}
	must(t, f.Store.Update(func(b *board.Board) error { b.Agents["a1"].Profile, b.Agents["a1"].SessionID = "cl", "s16"; return nil }))
	if got, err := f.LastMessage("a1"); err != nil || got != "1\n2\n3" {
		t.Fatalf("claude: %q %v", got, err)
	}

	tr := acphost.TranscriptPath(f.Home, "a1")
	must(t, os.MkdirAll(filepath.Dir(tr), 0o700))
	must(t, os.WriteFile(tr, []byte(`{"kind":"agent","text":"from acp"}`+"\n"+`{"kind":"tool","title":"Run ls"}`+"\n"), 0o600))
	must(t, f.Store.Update(func(b *board.Board) error { b.Agents["a1"].Driver = board.DriverACP; return nil }))
	got, err = f.LastMessage("a1")
	must(t, f.Store.Update(func(b *board.Board) error { b.Agents["a1"].Driver = ""; return nil }))
	if err != nil || got != "from acp" {
		t.Fatalf("acp: %q %v", got, err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
