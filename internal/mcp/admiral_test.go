package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/supervisor"
)

func TestAdmiralProfileTool(t *testing.T) {
	s, _, _ := memServer(t)
	var swapped []string
	s.SwapProfile = func(name string) error { swapped = append(swapped, name); return nil }

	if out, isErr := callText(t, s, "fleet_admiral_profile", map[string]any{"profile": "nope"}); !isErr || !strings.Contains(out, `no profile "nope"`) || !strings.Contains(out, "copilot") {
		t.Errorf("unknown: %q", out)
	}
	if len(swapped) != 0 {
		t.Fatalf("unknown name scheduled a swap: %v", swapped)
	}
	out, isErr := callText(t, s, "fleet_admiral_profile", map[string]any{"profile": "copilot", "summary": "user wants claude next"})
	if isErr || out != "switching admiral to copilot in a few seconds. Stop here." {
		t.Errorf("swap: %q", out)
	}
	if len(swapped) != 1 || swapped[0] != "copilot" {
		t.Errorf("SwapProfile calls: %v", swapped)
	}
	if h, _, _ := supervisor.ReadHandoff(s.F.Home); !strings.Contains(h, "user wants claude next") {
		t.Errorf("handoff %q", h)
	}
}

func TestAdmiralHeader(t *testing.T) {
	s, _, _ := memServer(t)
	if st, _ := callText(t, s, "fleet_status", nil); !strings.HasPrefix(st, "admiral: ?\n") {
		t.Errorf("no session: %q", st)
	}
	supervisor.Save(s.F.Home, supervisor.Session{SessionID: "3f2a91c0-aaaa", Profile: "claude-personal", Generation: 3, Started: time.Now()})
	const want = "admiral: claude-personal · session 3f2a91c · gen 3\n"
	if st, _ := callText(t, s, "fleet_status", nil); !strings.HasPrefix(st, want) {
		t.Errorf("status: %q", st)
	}
	br, _ := callText(t, s, "fleet_brief", nil)
	if !strings.HasPrefix(br, want) || strings.Count(br, "admiral: ") != 1 {
		t.Errorf("brief: %q", br)
	}
}

// ADR-159: an external admiral (Claude Desktop) has no pane, so the tools
// that replace one are refused before they touch tmux, and it gets the
// short instructions with the full rules behind fleet_instructions.
func TestExternalAdmiral(t *testing.T) {
	s, pct, resets := memServer(t)
	s.Admiral, s.External = "desktop", true
	swaps := 0
	s.SwapProfile = func(string) error { swaps++; return nil }
	*pct = 99
	for tool, a := range map[string]map[string]any{
		"fleet_handoff":         {"summary": "x"},
		"fleet_admiral_profile": {"profile": "copilot"},
	} {
		if out, isErr := callText(t, s, tool, a); !isErr || !strings.Contains(out, "external") {
			t.Errorf("%s: %q", tool, out)
		}
	}
	if *resets != 0 || swaps != 0 {
		t.Errorf("resets %d swaps %d, want none", *resets, swaps)
	}
	if st, _ := callText(t, s, "fleet_status", nil); !strings.HasPrefix(st, "admiral desktop: external") || strings.Contains(st, "fleet_handoff]") {
		t.Errorf("status: %q", st)
	}
	if s.instructions() != ExternalPointer || !strings.Contains(s.currentInstructions(nil), Instructions) {
		t.Error("external admiral: want the pointer at initialize and the full rules from fleet_instructions")
	}
}
