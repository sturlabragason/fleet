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
	"github.com/sturlabragason/fleet/internal/playbook"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/wall"
)

func memServer(t *testing.T) (*Server, *int, *int) {
	t.Helper()
	t.Setenv("FLEET_ADMIRAL_HANDOFF_PCT", "")
	t.Setenv("FLEET_SUP_HANDOFF_PCT", "")
	t.Setenv("FLEET_ADMIRAL_HANDOFF_TOKENS", "0")
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
	if !strings.HasSuffix(out, "\n\n[admiral context 74%, 148k tokens: "+warnTail) {
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

// On a big window the warning comes at a fixed size, well below the
// percent threshold (ADR-93).
func TestContextWarningByTokens(t *testing.T) {
	s, _, _ := memServer(t)
	t.Setenv("FLEET_ADMIRAL_HANDOFF_TOKENS", "")
	used := 0
	s.Fill = func() (int, int, bool) { return used, 1_000_000, true }
	for _, c := range []struct {
		used int
		want bool
	}{{79_000, false}, {80_000, true}} {
		used = c.used
		out, _ := callText(t, s, "fleet_profiles", nil)
		if strings.Contains(out, warnTail) != c.want {
			t.Errorf("%d tokens: warning %v in %q", c.used, !c.want, out)
		}
	}
	if out, _ := callText(t, s, "fleet_profiles", nil); !strings.Contains(out, "[admiral context 8%, 80k tokens: ") {
		t.Errorf("warning must name the size: %q", out)
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

// fleet_note rule=true makes the note a standing rule in the user layer, so
// the next spawn's playbook carries it at every level (ADR-131).
func TestNoteRuleReachesPlaybook(t *testing.T) {
	s, _, _ := memServer(t)
	out, isErr := callText(t, s, "fleet_note", map[string]any{"text": "never send into a session I am typing in", "rule": true, "pin": true})
	if isErr || !strings.Contains(out, "now in every new spawn") {
		t.Fatalf("note rule: %q", out)
	}
	for _, l := range playbook.Levels {
		if text, _ := playbook.Resolve(l, s.F.Home, t.TempDir()); !strings.Contains(text, "- never send into a session I am typing in") {
			t.Errorf("%s: playbook lacks the rule", l)
		}
	}
	if p := supervisor.Pinned(s.F.Home); len(p) != 1 {
		t.Errorf("pinned %q", p)
	}
	if out, _ := callText(t, s, "fleet_note", map[string]any{"text": "Never send into a session I am typing in", "rule": true}); !strings.Contains(out, "already") {
		t.Errorf("duplicate rule: %q", out)
	}
}

// A pinned note stays in the brief after more than 30 later notes, and is
// not repeated in the recent notes (ADR-62).
func TestBriefShowsPinnedNotes(t *testing.T) {
	s, _, _ := memServer(t)
	if _, isErr := callText(t, s, "fleet_note", map[string]any{"text": "User rule: ask before merging", "pin": true}); isErr {
		t.Fatal("pinned note failed")
	}
	for i := range 31 {
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

// A large state still gives a brief under briefBudget with the handoff and
// every pinned note whole; cut sections name the tool with the rest.
func TestBriefStaysUnderBudget(t *testing.T) {
	s, _, _ := memServer(t)
	home := s.F.Home
	handoff := "HANDOFF-START " + strings.Repeat("plan ", 780) + "HANDOFF-END"
	if err := supervisor.WriteHandoffOf(home, s.Me(), handoff); err != nil {
		t.Fatal(err)
	}
	for i := range 80 {
		callText(t, s, "fleet_route", map[string]any{"action": "set", "name": fmt.Sprintf("route%02d", i), "fields": map[string]any{
			"triggers": []any{strings.Repeat("a long trigger phrase ", 5)}, "dir": home}})
	}
	for i := range 8 {
		callText(t, s, "fleet_note", map[string]any{"text": fmt.Sprintf("PIN-%d %s", i, strings.Repeat("rule ", 50)+"end"), "pin": true})
	}
	for i := range 200 {
		callText(t, s, "fleet_note", map[string]any{"text": fmt.Sprintf("note-%03d %s", i, strings.Repeat("x", 300))})
	}
	out, err := s.brief()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > briefBudget {
		t.Errorf("brief is %d bytes, budget %d", len(out), briefBudget)
	}
	if !strings.HasPrefix(out, "## Handoff (") || !strings.Contains(out, handoff+"\n") {
		t.Error("handoff not whole and first")
	}
	for i := range 8 {
		if !strings.Contains(out, fmt.Sprintf("PIN-%d %s\n", i, strings.Repeat("rule ", 50)+"end")) {
			t.Errorf("pinned note %d cut", i)
		}
	}
	for _, want := range []string{"note-199 ", "fleet_recall", "fleet_route list"} {
		if !strings.Contains(out, want) {
			t.Errorf("brief missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "note-150 ") {
		t.Error("older notes should have been cut")
	}

	// Pinned notes over half the budget are still shown whole, with a warning.
	for i := range 30 {
		callText(t, s, "fleet_note", map[string]any{"text": fmt.Sprintf("BIG-%d %s", i, strings.Repeat("y", 300)), "pin": true})
	}
	out, _ = s.brief()
	if !strings.Contains(out, "BIG-0 ") || !strings.Contains(out, "BIG-29 ") || !strings.Contains(out, "ask the user which rules to retire") ||
		!strings.Contains(out, "## Unread reports\nnone\n") || !strings.Contains(out, "## Agents\nno agents\n") {
		t.Errorf("big pinned brief:\n%s", out)
	}
}
