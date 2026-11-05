package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/team"
)

// crowd is 20 agents: 3 waiting, one working on the wall, 10 working and
// 6 idle, in a w×14 overview with a fixed clock.
func crowd(w int) (*Model, *fakeBE) {
	zone.NewGlobal()
	now := time.Now()
	be := &fakeBE{}
	m := New(be, "")
	m.now = func() time.Time { return now }
	m.Update(tea.WindowSizeMsg{Width: w, Height: 14})
	ag := func(name, st string, age time.Duration) *board.Agent {
		return &board.Agent{Name: name, Profile: "fake", Status: st, StatusSince: now.Add(-age)}
	}
	as := []*board.Agent{ag("wall", "working", time.Minute)}
	for i := 0; i < 3; i++ {
		as = append(as, ag(fmt.Sprintf("w%d", i), "waiting", time.Duration(10-i)*time.Minute))
	}
	for i := 0; i < 10; i++ {
		as = append(as, ag(fmt.Sprintf("r%02d", i), "working", time.Minute))
	}
	for i := 0; i < 6; i++ {
		as = append(as, ag(fmt.Sprintf("i%d", i), "idle", time.Hour))
	}
	m.onWall = map[string]bool{"wall": true}
	m.setAgents(as)
	return m, be
}

func names(m *Model) string {
	var n []string
	for _, a := range m.agents {
		n = append(n, a.Name)
	}
	return strings.Join(n, ",")
}

// With every card fitting nothing changes: cards only, no rows or fold.
func TestTiersOffWhenCardsFit(t *testing.T) {
	_, v := overview(130, 14, 6)
	if cards(v) != 6 || strings.Contains(v, "idle") || strings.Contains(v, "more") {
		t.Errorf("6 agents must be 6 cards as before:\n%s", v)
	}
}

func TestTiersCardsAndRows(t *testing.T) {
	m, _ := crowd(150)
	v := ansi.Strip(m.View())
	if got := cards(v); got != 4 {
		t.Errorf("want cards for 3 waiting + 1 on the wall, got %d:\n%s", got, v)
	}
	lines := strings.Split(v, "\n")
	if len(lines) != 1+6+4 { // header, cards, 16 rows in 4 columns
		t.Errorf("want 11 lines, got %d", len(lines))
	}
	for _, l := range lines {
		if ansi.StringWidth(l) > 150 {
			t.Errorf("line wider than the pane: %q", l)
		}
	}
	for _, a := range m.agents {
		if !strings.Contains(v, a.Name) {
			t.Errorf("%s missing:\n%s", a.Name, v)
		}
	}
	if !strings.Contains(lines[7], "⠋ r00") || !strings.Contains(lines[7], " 1m") {
		t.Errorf("first row line: %q", lines[7])
	}
	if !strings.HasPrefix(names(m), "w0,w1,w2,wall,r00") {
		t.Errorf("order %s", names(m))
	}
}

// A click on a row does what it does on a card.
func TestClickOnARow(t *testing.T) {
	m, be := crowd(80) // 3 cards: wall is the first row
	m.View()
	time.Sleep(50 * time.Millisecond) // zones register asynchronously
	_, cmd := m.Update(tea.MouseMsg{X: 5, Y: 7, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if m.sel != "wall" || cmd == nil {
		t.Fatalf("sel=%s cmd=%v", m.sel, cmd)
	}
	cmd()
	if len(be.shown) != 1 || be.shown[0] != "wall" {
		t.Errorf("shown %v", be.shown)
	}
	m.Update(tea.MouseMsg{X: 45, Y: 7, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	if _, cmd := m.Update(tea.MouseMsg{X: 45, Y: 7, Action: tea.MouseActionRelease, Button: tea.MouseButtonRight}); m.sel != "r00" || cmd == nil {
		t.Fatalf("right click on a row: sel=%s", m.sel)
	}
}

func TestFoldChipAndWheel(t *testing.T) {
	m, _ := crowd(80) // 2 row columns, 7 lines: 17 rows don't fit
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "+6 idle") || strings.Contains(v, "i0") {
		t.Fatalf("idle agents should fold:\n%s", v)
	}
	time.Sleep(50 * time.Millisecond)
	// wall + 10 working + chip: the chip is cell 11, line 5, column 1
	m.Update(tea.MouseMsg{X: 45, Y: 12, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if !m.unfold {
		t.Fatal("clicking the chip unfolds")
	}
	v = ansi.Strip(m.View())
	if !strings.Contains(v, "↓ 4 more") || strings.Contains(v, "↑") {
		t.Errorf("unfolded rows overflow downwards:\n%s", v)
	}
	for i := 0; i < 3; i++ {
		m.Update(tea.MouseMsg{X: 5, Y: 10, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	}
	if m.rowOff != 2 {
		t.Errorf("wheel scrolls to the end and stops: rowOff=%d", m.rowOff)
	}
	v = ansi.Strip(m.View())
	for _, want := range []string{"↑ 5 more", "i5", "− fold idle"} {
		if !strings.Contains(v, want) {
			t.Errorf("scrolled to the end, missing %q:\n%s", want, v)
		}
	}
	m.Update(tea.MouseMsg{X: 5, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if m.rowOff != 2 {
		t.Error("the wheel over the cards does not scroll the rows")
	}
	m.sel = foldSel
	m.key("enter")
	if m.unfold {
		t.Error("Enter on the chip folds again")
	}
}

func TestTierKeys(t *testing.T) {
	m, _ := crowd(80) // cards w0 w1 w2; rows wall r00 / r01 r02 / ...
	m.sel = "w0"
	for _, step := range []struct{ key, want string }{
		{"right", "w1"}, {"down", "r00"}, {"down", "r02"}, {"left", "r01"}, {"up", "wall"},
		{"right", "r00"}, {"up", "w1"}, {"left", "w0"}, {"down", "wall"}, {"up", "w0"},
	} {
		m.key(step.key)
		if m.sel != step.want {
			t.Fatalf("%s: sel=%s, want %s", step.key, m.sel, step.want)
		}
	}
	m.unfold = true // 9 lines in 7: moving down to the last scrolls
	for i := 0; i < 10; i++ {
		m.key("down")
	}
	if m.sel != "i5" || m.rowOff != 2 {
		t.Errorf("down to the last line: sel=%q rowOff=%d", m.sel, m.rowOff)
	}
	m.key("right")
	if v := ansi.Strip(m.View()); m.sel != foldSel || m.rowOff != 2 || !strings.Contains(v, "− fold idle") {
		t.Errorf("sel=%q rowOff=%d:\n%s", m.sel, m.rowOff, v)
	}
}

func TestTiersNarrowPane(t *testing.T) {
	for _, w := range []int{10, 25, 31} {
		m, _ := crowd(w)
		v := ansi.Strip(m.View())
		lines := strings.Split(v, "\n")
		if len(lines) > 14 {
			t.Errorf("w=%d: %d lines", w, len(lines))
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > w {
				t.Errorf("w=%d: line too wide: %q", w, l)
			}
		}
		for i := 0; i < 30; i++ {
			m.key("down")
		}
		m.View()
	}
}

func TestStableOrderFreezes(t *testing.T) {
	zone.NewGlobal()
	now := time.Now()
	m := New(&fakeBE{}, "")
	m.now = func() time.Time { return now }
	m.Update(tea.WindowSizeMsg{Width: 150, Height: 14})
	poll := func(echo string, extra ...string) {
		as := []*board.Agent{
			{Name: "bravo", Status: "waiting", StatusSince: now.Add(-time.Hour)},
			{Name: "alpha", Status: "working"},
			{Name: "echo", Status: echo, StatusSince: now},
			{Name: "delta", Status: "working"},
		}
		for _, n := range extra {
			as = append(as, &board.Agent{Name: n, Status: "working"})
		}
		m.setAgents(as)
	}
	poll("idle")
	if got := names(m); got != "bravo,alpha,delta,echo" {
		t.Fatalf("first order %s", got)
	}
	m.Update(tea.MouseMsg{X: 1, Y: 1, Action: tea.MouseActionMotion})
	now = now.Add(time.Second)
	poll("waiting")
	if got := names(m); got != "bravo,alpha,delta,echo" {
		t.Errorf("within 3s of the pointer nothing moves: %s", got)
	}
	now = now.Add(3 * time.Second)
	poll("waiting")
	if got := names(m); got != "bravo,echo,alpha,delta" {
		t.Errorf("after 3s echo joins the end of the waiting tier: %s", got)
	}
	poll("waiting", "aaron")
	if got := names(m); got != "bravo,echo,alpha,delta,aaron" {
		t.Errorf("a new agent goes to the end of its tier: %s", got)
	}
	poll("idle", "aaron")
	if got := names(m); got != "bravo,alpha,delta,aaron,echo" {
		t.Errorf("echo back to idle: %s", got)
	}
}

func TestTeamMembersSitTogether(t *testing.T) {
	m := New(&fakeBE{}, "")
	m.teams = map[string]team.Info{"a2": {Team: "x"}, "x1": {Team: "x"}}
	m.setAgents([]*board.Agent{{Name: "x1", Status: "working"}, {Name: "b", Status: "working"}, {Name: "a2", Status: "working"}})
	if got := names(m); got != "a2,x1,b" {
		t.Errorf("order %s", got)
	}
}

// A folded selected agent is drawn as the chip, so Enter there unfolds
// instead of showing the hidden agent.
func TestEnterOnChipStandingInForAFoldedAgent(t *testing.T) {
	m, _ := crowd(80)
	m.View()
	m.sel = "i0"
	m.key("enter")
	if !m.unfold {
		t.Error("Enter on the chip unfolds")
	}
}
