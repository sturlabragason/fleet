package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/layout"
	"github.com/sturlabragason/fleet/internal/status"
)

func subs(states ...string) []status.Subagent {
	t0 := time.Now().Add(-time.Hour)
	var l []status.Subagent
	for i, s := range states {
		l = append(l, status.Subagent{ID: fmt.Sprint(i), Name: fmt.Sprint(i), State: s, Started: t0.Add(time.Duration(i) * time.Second)})
	}
	return l
}

func TestSubagentDotColours(t *testing.T) {
	r, d, f := status.SubRunning, status.SubDone, status.SubFailed
	cols, hidden := subagentDots(subs(r, r, r, d, f), 20)
	if len(cols) != 5 || hidden != 0 {
		t.Fatalf("%d dots, %d hidden", len(cols), hidden)
	}
	want := []lipgloss.Color{colWorking, colWorking, colWorking, colIdle, colError}
	for i, c := range cols {
		if c != want[i] {
			t.Errorf("dot %d: %v, want %v", i, c, want[i])
		}
	}
}

func TestSubagentRowOverflow(t *testing.T) {
	l := subs(strings.Split(strings.Repeat("done ", 12), " ")[:12]...)
	l[11].State = status.SubRunning
	// 12 dots need 23 columns: the newest 9 fit, then "+3".
	if row := ansi.Strip(subagentRow(l, 20)); row != "• • • • • • • • • +3" {
		t.Errorf("%q", row)
	}
	cols, hidden := subagentDots(l, 20)
	if hidden != 3 || cols[len(cols)-1] == colIdle {
		t.Errorf("the newest (running) dot must be shown: hidden %d", hidden)
	}
	if subagentRow(nil, 20) != "" {
		t.Error("row without subagents")
	}
}

func TestTileSubagentRow(t *testing.T) {
	zone.NewGlobal()
	now := time.Now()
	m := New(&fakeBE{}, "")
	m.now = func() time.Time { return now }
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 9})
	r, d, f := status.SubRunning, status.SubDone, status.SubFailed
	a := &board.Agent{Name: "orch", Profile: "copilot", Status: "working", LastLine: "running go test", StatusSince: now.Add(-time.Minute)}
	plain := ansi.Strip(m.tile(a, m.cfg.TileW))
	if strings.Contains(plain, "•") {
		t.Errorf("dots without subagents:\n%s", plain)
	}
	a.Subagents = subs(r, r, r, d, f)
	lines := strings.Split(ansi.Strip(m.tile(a, m.cfg.TileW)), "\n")
	if len(lines) != layout.Default.TileH {
		t.Fatalf("tile has %d lines:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[4], "• • • • •") || ansi.StringWidth(lines[4]) != ansi.StringWidth(lines[0]) {
		t.Errorf("line 4 %q", lines[4])
	}
}
