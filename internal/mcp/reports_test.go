package mcp

import (
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/wall"
)

func TestReportRoundTrip(t *testing.T) {
	f := mailHome(t)
	var pasted []string
	box := ""
	f.SupPane = func() fleet.AdmiralView { return fleet.AdmiralView{Pane: "%sup", Idle: true, Box: true, Input: box} }
	f.TypeText = func(pane, text string) error { box += text; return nil }
	f.PressKeys = func(pane string, keys ...string) error { pasted = append(pasted, box); box = ""; return nil }
	ra := startRPC(t, NewAgent(f, "a"))
	sup := startRPC(t, New(f, wall.New(f.T, fleet.Session)))
	sup.call("initialize", map[string]any{"protocolVersion": ProtocolVersion})

	if msg, isErr := ra.tool("fleet_report", map[string]any{"state": "finished", "summary": "x"}); !isErr || !strings.Contains(msg, "bad state") {
		t.Fatalf("bad state: %q", msg)
	}
	if msg, isErr := ra.tool("fleet_report", map[string]any{"state": "done", "summary": "nav fixed on feat/nav"}); isErr || msg != "reported done to the admiral" {
		t.Fatalf("report: %q", msg)
	}
	if len(pasted) != 1 || pasted[0] != "[fleet] report from a: done: nav fixed on feat/nav. Full text: call fleet_reports." {
		t.Fatalf("wake: %q", pasted)
	}
	st, _ := sup.tool("fleet_status", nil)
	if !strings.Contains(st, "| report\n") || !strings.Contains(st, "| done: nav fixed on feat/nav\n") {
		t.Fatalf("status report column: %q", st)
	}
	if !strings.HasSuffix(st, "\n\n(1 unread reports; fleet_reports reads them)\n- a done: nav fixed on feat/nav") {
		t.Fatalf("status suffix: %q", st)
	}
	br, _ := sup.tool("fleet_brief", nil)
	if !strings.Contains(br, "## Unread reports\n") || !strings.Contains(br, "a: done: nav fixed") || !strings.HasSuffix(br, "(1 unread reports; fleet_reports reads them)\n- a done: nav fixed on feat/nav") {
		t.Fatalf("brief must list and not mark read: %q", br)
	}
	msg, isErr := sup.tool("fleet_reports", nil)
	if isErr || !strings.Contains(msg, " a: done: nav fixed on feat/nav\n") || strings.Contains(msg, "unread reports") {
		t.Fatalf("reports: %q", msg)
	}
	if msg, _ := sup.tool("fleet_reports", nil); msg != "no new reports" {
		t.Fatalf("second reports: %q", msg)
	}
	if msg, _ := sup.tool("fleet_reports", map[string]any{"all": true}); !strings.Contains(msg, "nav fixed") {
		t.Fatalf("reports all: %q", msg)
	}
	long := "split done\nsecond line: " + strings.Repeat("detail ", 40)
	ra.tool("fleet_report", map[string]any{"state": "blocked", "summary": long})
	if br, _ := sup.tool("fleet_brief", nil); !strings.Contains(br, "a: blocked: split done second line: detail") || strings.Contains(br, "\n    second line") || !strings.Contains(br, "…\n") {
		t.Fatalf("brief must show one capped headline: %q", br)
	}
	if msg, _ := sup.tool("fleet_reports", nil); !strings.Contains(msg, "a: blocked: split done\n    second line: detail") || !strings.Contains(msg, strings.Repeat("detail ", 39)+"detail\n") {
		t.Fatalf("reports must show full indented text: %q", msg)
	}
	ra.tool("fleet_report", map[string]any{"state": "progress", "summary": "tests next"})
	if len(pasted) != 2 {
		t.Fatalf("progress woke the admiral: %q", pasted)
	}
	if msg, _ := sup.tool("fleet_handoff", map[string]any{}); strings.Contains(msg, "unread reports") {
		t.Fatalf("handoff got the suffix: %q", msg)
	}
}

func TestReportsByNameHeadlinesAndCards(t *testing.T) {
	f := mailHome(t)
	f.Store.Update(func(b *board.Board) error {
		b.Agents["a"].Card = &board.Card{Goal: "goal-a"}
		b.Agents["b"].Card = &board.Card{Goal: "goal-b"}
		return nil
	})
	sup := startRPC(t, New(f, wall.New(f.T, fleet.Session)))
	sup.call("initialize", map[string]any{"protocolVersion": ProtocolVersion})
	long := "a first\nsecond line " + strings.Repeat("x", 200)
	f.Mail().Report("a", "done", long)
	f.Mail().Report("b", "blocked", "b needs a key")
	f.Mail().Report("a", "progress", strings.Repeat("y", 300))

	msg, _ := sup.tool("fleet_reports", map[string]any{"name": "a"})
	if !strings.Contains(msg, "a: done: a first\n    second line") || strings.Contains(msg, "b: blocked") ||
		!strings.Contains(msg, "| goal-a |") || strings.Contains(msg, "goal-b") {
		t.Fatalf("name=a: %q", msg)
	}
	if msg, _ := sup.tool("fleet_reports", map[string]any{"name": "a"}); !strings.HasPrefix(msg, "no new reports from a") || strings.Contains(msg, "Task cards") {
		t.Fatalf("name=a again: %q", msg)
	}
	msg, _ = sup.tool("fleet_reports", map[string]any{"all": true})
	if strings.Contains(msg, "second line") || !strings.Contains(msg, "a: done: a first\n") || !strings.Contains(msg, strings.Repeat("y", 100)+"…\n") ||
		strings.Contains(msg, strings.Repeat("y", 120)) || !strings.Contains(msg, "name=<agent>") || !strings.Contains(msg, "goal-b") {
		t.Fatalf("all headlines: %q", msg)
	}
	if msg, _ := sup.tool("fleet_reports", map[string]any{"all": true, "name": "a"}); !strings.Contains(msg, "second line") || strings.Contains(msg, "b: blocked") || strings.Contains(msg, "goal-b") {
		t.Fatalf("all name=a: %q", msg)
	}
	msg, _ = sup.tool("fleet_reports", nil)
	if !strings.Contains(msg, "b: blocked: b needs a key") || strings.Contains(msg, "a first") || strings.Contains(msg, "goal-a") {
		t.Fatalf("b must still be unread, alone: %q", msg)
	}
	if msg, _ := sup.tool("fleet_reports", nil); msg != "no new reports" {
		t.Fatalf("no new reports must carry no cards: %q", msg)
	}
}
