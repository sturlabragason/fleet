package mcp

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/wall"
)

func memServer(t *testing.T) (*Server, *int, *int) {
	t.Helper()
	t.Setenv("FLEET_ADMIRAL_HANDOFF_PCT", "")
	t.Setenv("FLEET_SUP_HANDOFF_PCT", "")
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := fleet.New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	s := New(f, wall.New(tc, fleet.Session))
	pct, resets := new(int), new(int)
	*pct = -1 // unknown
	s.Fill = func() (int, int, bool) {
		if *pct < 0 {
			return 0, 0, false
		}
		return *pct * 2000, 200000, true
	}
	s.Reset = func() error { *resets++; return nil }
	return s, pct, resets
}

func callText(t *testing.T, s *Server, name string, a map[string]any) (string, bool) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"name": name, "arguments": a})
	res, rerr := s.handle(request{Method: "tools/call", Params: raw})
	if rerr != nil {
		t.Fatalf("%s: %v", name, rerr.Message)
	}
	m := res.(map[string]any)
	return m["content"].([]map[string]any)[0]["text"].(string), m["isError"].(bool)
}

const warnTail = "save anything pending with fleet_note, then call fleet_handoff]"

func TestContextWarning(t *testing.T) {
	s, pct, _ := memServer(t)
	for _, c := range []struct {
		pct  int
		want bool
	}{{-1, false}, {69, false}, {70, true}, {74, true}} {
		*pct = c.pct
		out, _ := callText(t, s, "fleet_profiles", nil)
		if n := strings.Count(out, warnTail); (n == 1) != c.want || n > 1 {
			t.Errorf("pct %d: %d warnings in %q", c.pct, n, out)
		}
	}
	*pct = 74
	out, _ := callText(t, s, "fleet_peek", map[string]any{"name": "nobody"})
	if !strings.HasSuffix(out, "\n\n[admiral context 74%: "+warnTail) {
		t.Errorf("error result needs the warning too: %q", out)
	}
	out, _ = callText(t, s, "fleet_handoff", map[string]any{"summary": "x"})
	if strings.Contains(out, warnTail) {
		t.Errorf("fleet_handoff must not carry the warning: %q", out)
	}
	s.SwapProfile = func(string) error { return nil }
	out, _ = callText(t, s, "fleet_admiral_profile", map[string]any{"profile": "copilot"})
	if strings.Contains(out, warnTail) {
		t.Errorf("fleet_admiral_profile must not carry the warning: %q", out)
	}
}

func TestNoteBriefHandoff(t *testing.T) {
	s, _, resets := memServer(t)
	home := s.F.Home
	if _, isErr := callText(t, s, "fleet_note", map[string]any{"text": "tell user when web-nav is done", "agent": "web-nav"}); isErr {
		t.Fatal("note failed")
	}
	b, _ := os.ReadFile(filepath.Join(home, "decisions.log"))
	if !strings.HasSuffix(string(b), " [web-nav] tell user when web-nav is done\n") {
		t.Errorf("decisions.log %q", b)
	}
	if _, isErr := callText(t, s, "fleet_note", map[string]any{"text": " "}); !isErr {
		t.Error("empty note must fail")
	}

	if _, isErr := callText(t, s, "fleet_handoff", map[string]any{"summary": "asked user about deploy; waiting"}); isErr {
		t.Fatal("handoff failed")
	}
	if *resets != 1 {
		t.Errorf("resets %d", *resets)
	}
	if h, _, ok := supervisor.ReadHandoff(home); !ok || h != "asked user about deploy; waiting" {
		t.Errorf("handoff.md %q", h)
	}
	if _, isErr := callText(t, s, "fleet_handoff", map[string]any{}); !isErr || *resets != 1 {
		t.Error("handoff without summary must fail and not reset")
	}

	out, isErr := callText(t, s, "fleet_brief", nil)
	for _, want := range []string{"## Handoff (", "asked user about deploy", "## Notes", "[web-nav] tell user", "## Agents", "no agents"} {
		if isErr || !strings.Contains(out, want) {
			t.Errorf("brief missing %q:\n%s", want, out)
		}
	}
}

// A pinned note stays in the brief after more than 30 later notes, and is
// not repeated in the recent notes (ADR-62).
func TestBriefShowsPinnedNotes(t *testing.T) {
	s, _, _ := memServer(t)
	if _, isErr := callText(t, s, "fleet_note", map[string]any{"text": "User rule: ask before merging", "pin": true}); isErr {
		t.Fatal("pinned note failed")
	}
	for i := 0; i < 31; i++ {
		callText(t, s, "fleet_note", map[string]any{"text": fmt.Sprintf("note %d", i)})
	}
	out, _ := callText(t, s, "fleet_brief", nil)
	if !strings.Contains(out, "## Pinned\n") || strings.Count(out, "ask before merging") != 1 {
		t.Fatalf("brief:\n%s", out)
	}
	callText(t, s, "fleet_note", map[string]any{"text": "User rule: two", "pin": true})
	out, _ = callText(t, s, "fleet_brief", nil)
	if strings.Count(out, "User rule: two") != 1 {
		t.Fatalf("a recent pinned note was shown twice:\n%s", out)
	}
}

func TestResetShellUsesAdmiral(t *testing.T) {
	if got := resetShell("'/x/fleet'"); got != "sleep 2; '/x/fleet' admiral reset --handoff >/dev/null 2>&1" {
		t.Errorf("%q", got)
	}
}
