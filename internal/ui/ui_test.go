package ui

import (
	"fmt"

	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest"
	zone "github.com/lrstanley/bubblezone"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/keys"
	"github.com/sturlabragason/fleet/internal/layout"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/team"
	"github.com/sturlabragason/fleet/internal/wall"
)

type fakeBE struct {
	shown, hidden []string
	added         []string
	nexts         []int
	nextName      string // what Next returns
	nextRefuse    bool   // Next finds a waiting agent but the wall refuses it
	target        string // agent in the ▶ slot
	untargets     int
	addRefuse     bool
	applied       int
	refuse        bool
	noWall        bool
	ctx           [2]string
	ctxSets       [][2]string
	resets        int
	picks         int
	changes       int
	teams         map[string]team.Info
	undos         int
	undoMsg       string // what Undo returns
	undoTarget    string
	pins          []string
	pinned        map[string]bool
	previews      []string
	menus         []string // "name x,y"
	reports       int      // unread admiral reports the header badge shows
}

func (f *fakeBE) Refresh() ([]*board.Agent, error) {
	t0 := time.Now().Add(-time.Minute)
	return []*board.Agent{
		{Name: "alpha", Profile: "fake", Status: "working", LastLine: "thinking", StatusSince: t0},
		{Name: "bravo", Profile: "fake", Status: "waiting", LastLine: "Approve? [y/n]", StatusSince: t0},
		{Name: "charlie", Profile: "claude", Status: "error", LastLine: "✖ Error", StatusSince: t0},
		{Name: "delta", Profile: "fake", Status: "waiting", LastLine: "Approve? [y/n]", StatusSince: t0.Add(-time.Hour)},
		{Name: "echo", Profile: "fake", Status: "idle", StatusSince: t0},
	}, nil
}
func (f *fakeBE) OnWall() map[string]bool     { return map[string]bool{"alpha": true} }
func (f *fakeBE) Teams() map[string]team.Info { return f.teams }
func (f *fakeBE) Show(n string) (bool, error) {
	f.shown = append(f.shown, n)
	return !f.refuse, nil
}
func (f *fakeBE) Add(n string) (bool, error) {
	f.added = append(f.added, n)
	return !f.addRefuse, nil
}
func (f *fakeBE) Next(dir int) (string, bool, error) {
	f.nexts = append(f.nexts, dir)
	return f.nextName, !f.nextRefuse, nil
}
func (f *fakeBE) Target() string           { return f.target }
func (f *fakeBE) Untarget() error          { f.untargets++; f.target = ""; return nil }
func (f *fakeBE) Hide(n string) error      { f.hidden = append(f.hidden, n); return nil }
func (f *fakeBE) Preset(string) error      { return nil }
func (f *fakeBE) WallEnabled() bool        { return !f.noWall }
func (f *fakeBE) ApplyWall() error         { f.applied++; return nil }
func (f *fakeBE) SupCtx() (string, string) { return f.ctx[0], f.ctx[1] }
func (f *fakeBE) SetSupCtx(l, c string) error {
	f.ctxSets = append(f.ctxSets, [2]string{l, c})
	return nil
}
func (f *fakeBE) ResetSup() error { f.resets++; return nil }
func (f *fakeBE) Reports() int    { return f.reports }
func (f *fakeBE) Undo() (string, string, error) {
	f.undos++
	return f.undoMsg, f.undoTarget, nil
}
func (f *fakeBE) TogglePin(n string) (bool, error) {
	f.pins = append(f.pins, n)
	if f.pinned == nil {
		f.pinned = map[string]bool{}
	}
	f.pinned[n] = !f.pinned[n]
	return f.pinned[n], nil
}
func (f *fakeBE) Pinned() map[string]bool { return f.pinned }
func (f *fakeBE) Preview(n string) error  { f.previews = append(f.previews, n); return nil }
func (f *fakeBE) Menu(n string, x, y int) error {
	f.menus = append(f.menus, fmt.Sprintf("%s %d,%d", n, x, y))
	return nil
}
func (f *fakeBE) Changes() error { f.changes++; return nil }
func (f *fakeBE) PickProfile() error {
	f.picks++
	return nil
}

func TestOrderAndKeys(t *testing.T) {
	zone.NewGlobal()
	be := &fakeBE{}
	m := New(be, "")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(130, 14))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool { return strings.Contains(string(b), "charlie") },
		teatest.WithDuration(3*time.Second))
	// attention order: delta (longest wait), bravo, charlie, alpha, echo
	var names []string
	for _, a := range m.agents {
		names = append(names, a.Name)
	}
	if got := strings.Join(names, ","); got != "delta,bravo,charlie,alpha,echo" {
		t.Errorf("order %s", got)
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyRight}) // delta -> bravo
	tm.Send(tea.KeyMsg{Type: tea.KeyRight}) // -> charlie
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	if len(be.shown) != 1 || be.shown[0] != "charlie" {
		t.Errorf("shown %v", be.shown)
	}
	out, _ := io.ReadAll(tm.FinalOutput(t))
	_ = out
}

// Too many cards for a 60x9 pane: the agents that need you get cards, the
// rest rows, and what doesn't fit is counted.
func TestViewTilesThenRows(t *testing.T) {
	zone.NewGlobal()
	m := New(&fakeBE{}, "")
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 9}) // 2 cards, 2 row lines
	as, _ := (&fakeBE{}).Refresh()
	m.setAgents(as)
	v := ansi.Strip(m.View())
	for _, want := range []string{"delta", "bravo", "✖ charlie", "↓ 2 more", "5 agents"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
	if strings.Count(v, "╭")+strings.Count(v, "┏") != 2 {
		t.Errorf("want 2 tiles then rows:\n%s", v)
	}
}

func TestMouseClickSelectsAndShows(t *testing.T) {
	zone.NewGlobal()
	be := &fakeBE{}
	m := New(be, "")
	m.Update(tea.WindowSizeMsg{Width: 130, Height: 14})
	as, _ := be.Refresh()
	m.setAgents(as)
	m.View()
	time.Sleep(50 * time.Millisecond) // zones register asynchronously
	// 5 cards wrap 3+2 at 43 columns: the second (bravo) spans columns 43..85, rows 1..6
	_, cmd := m.Update(tea.MouseMsg{X: 50, Y: 3, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if m.sel != "bravo" || cmd == nil {
		t.Fatalf("sel=%s cmd=%v", m.sel, cmd)
	}
	cmd()
	if len(be.shown) != 1 || be.shown[0] != "bravo" {
		t.Errorf("shown %v", be.shown)
	}
}

func TestXHidesOnlyWallTiles(t *testing.T) {
	be := &fakeBE{}
	m := New(be, "")
	as, _ := be.Refresh()
	m.setAgents(as)
	m.onWall = be.OnWall()
	m.sel = "bravo"
	if cmd := m.key("x"); cmd != nil {
		t.Fatal("bravo is not on the wall")
	}
	m.sel = "alpha"
	m.key("x")()
	if len(be.hidden) != 1 || be.hidden[0] != "alpha" {
		t.Errorf("hidden %v", be.hidden)
	}
}

// Finding 1: nothing re-tiles the wall when a pane's status flips, so the
// refresh loop must apply the layout after every poll (cheap: cached).
func TestPollAppliesTheWall(t *testing.T) {
	be := &fakeBE{}
	m := New(be, "")
	msg := m.poll()()
	if _, ok := msg.(agentsMsg); !ok {
		t.Fatalf("poll returned %T", msg)
	}
	if be.applied != 1 {
		t.Errorf("wall applied %d times per poll, want 1", be.applied)
	}
}

// Finding 4: a refused Show (wall full, nothing evictable) must not be
// reported to the user as "on wall".
func TestShowRefusalIsReported(t *testing.T) {
	be := &fakeBE{refuse: true}
	m := New(be, "")
	m.sel = "alpha"
	got, ok := m.show()().(infoMsg)
	if !ok || string(got) != wall.FullMsg {
		t.Errorf("got %q, want %q", got, wall.FullMsg)
	}
	be.refuse = false
	if got := m.show()().(wallMsg); got.info != "alpha on wall" || got.target != "alpha" {
		t.Errorf("got %+v, want the already-on-wall message", got)
	}
	m.sel = "bravo"
	if got := m.show()().(wallMsg); got.info != "bravo added to wall" {
		t.Errorf("no target: got %q, want the append message", got.info)
	}
}

// DESIGN.md §5e: a card replacing the ▶ pane says which agent left.
func TestShowReportsTheReplacedAgent(t *testing.T) {
	be := &replacingBE{fakeBE: fakeBE{target: "alpha"}}
	m := New(be, "")
	m.sel = "bravo"
	got := m.show()().(wallMsg)
	if got.info != "bravo → ▶ (was alpha) · u undo" || got.target != "bravo" {
		t.Errorf("got %+v", got)
	}
	m.Update(got)
	if m.target != "bravo" || m.sel != "bravo" || m.msg != got.info {
		t.Errorf("after the message: target %q sel %q msg %q", m.target, m.sel, m.msg)
	}
}

// replacingBE swaps the shown agent into the target slot, like the wall.
type replacingBE struct{ fakeBE }

func (r *replacingBE) Show(n string) (bool, error) {
	r.shown = append(r.shown, n)
	r.target = n
	return true, nil
}
func (r *replacingBE) OnWall() map[string]bool { return map[string]bool{r.target: true} }

// Tab / Shift-Tab put the next/previous waiting agent into the ▶ slot and
// select it; a/+ add; Esc clears the target (and does nothing without one).
func TestTargetKeys(t *testing.T) {
	// foxtrot is off the fake wall, so Tab reads as a replace of the ▶ pane
	// (a ▶ pane still on the wall is pinned: TestShowReportsAPinnedTarget).
	be := &fakeBE{nextName: "delta", target: "foxtrot"}
	m := New(be, "")
	as, _ := be.Refresh()
	m.setAgents(as)
	m.Update(m.poll()())
	m.sel = "echo"
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m.Update(cmd())
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m.Update(cmd())
	if len(be.nexts) != 2 || be.nexts[0] != 1 || be.nexts[1] != -1 {
		t.Errorf("Next calls %v, want [1 -1]", be.nexts)
	}
	if m.sel != "delta" || m.target != "delta" {
		t.Errorf("sel %q target %q, want delta", m.sel, m.target)
	}
	be.nextName = ""
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m.Update(cmd())
	if m.msg != "nothing waiting" {
		t.Errorf("msg %q", m.msg)
	}
	for _, k := range []string{"a", "+"} {
		m.sel = "echo"
		_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		if got := cmd().(wallMsg); got.info != "echo added to wall" {
			t.Errorf("%s: %q", k, got.info)
		}
	}
	if len(be.added) != 2 || be.added[0] != "echo" {
		t.Errorf("added %v", be.added)
	}
	be.addRefuse = true
	if got := m.add()().(infoMsg); string(got) != wall.AddFullMsg {
		t.Errorf("refused add: %q", got)
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc with a target must clear it")
	}
	cmd()
	if be.untargets != 1 || m.target != "" || m.msg != "▶ cleared" {
		t.Errorf("untargets %d target %q msg %q", be.untargets, m.target, m.msg)
	}
	m.msg = ""
	if _, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc}); cmd != nil || m.msg != "" {
		t.Errorf("esc without a target: cmd %v msg %q", cmd != nil, m.msg)
	}
	// Esc with the ? overlay open only closes it.
	m.target = "alpha"
	m.help = true
	if _, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc}); cmd != nil || m.help || m.target != "alpha" {
		t.Errorf("esc in the overlay: cmd %v help %v target %q", cmd != nil, m.help, m.target)
	}
}

// The agent in the ▶ slot shows ▶ after its name on its card and its chip,
// and the header names it with the next actions.
func TestTargetCardAndHeader(t *testing.T) {
	zone.NewGlobal()
	be := &fakeBE{target: "alpha"}
	m := New(be, "")
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 9})
	m.Update(m.poll()())
	a := m.agents[3] // alpha, on the wall
	if a.Name != "alpha" {
		t.Fatalf("agent %s", a.Name)
	}
	tile := ansi.Strip(m.tile(a, m.cfg.TileW))
	if !strings.Contains(tile, "alpha ▶ ▣") {
		t.Errorf("tile:\n%s", tile)
	}
	if strings.Count(m.tile(a, m.cfg.TileW), "\n") != layout.Default.TileH-1 {
		t.Error("the ▶ must not wrap line 1")
	}
	if c := ansi.Strip(m.chips([]*board.Agent{a}, true)); !strings.Contains(c, "alpha ▶") {
		t.Errorf("chip %q", c)
	}
	if other := ansi.Strip(m.tile(m.agents[0], m.cfg.TileW)); strings.Contains(other, "▶") {
		t.Errorf("non-target tile has ▶:\n%s", other)
	}
	h := ansi.Strip(m.header())
	if !strings.Contains(h, "▶ alpha · click card replace · a add · Space preview · Tab next waiting · Esc clear · ? all keys") {
		t.Errorf("header %q", h)
	}
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 9})
	if h := ansi.Strip(m.header()); !strings.Contains(h, "▶ alpha · click card replace") || !strings.HasSuffix(h, "? all keys") || strings.Contains(h, "Esc clear") {
		t.Errorf("narrow header %q", h)
	}
	m.target = ""
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 9})
	if h := ansi.Strip(m.header()); !strings.Contains(h, "no ▶ pane · click a wall pane to pick one · ⏎ add/evict · Space preview · ? all keys") {
		t.Errorf("no-target header %q", h)
	}
	m.msg = "bravo → ▶ (was alpha) · u undo"
	if h := ansi.Strip(m.header()); !strings.Contains(h, m.msg) || strings.Contains(h, "all keys") {
		t.Errorf("message must win: %q", h)
	}
}

// A header message gives way to the hint line after msgTTL.
func TestMessageExpires(t *testing.T) {
	now := time.Unix(1000, 0)
	m := New(&fakeBE{}, "")
	m.now = func() time.Time { return now }
	m.Update(infoMsg("alpha added to wall"))
	m.Update(animMsg{})
	if m.msg == "" {
		t.Fatal("message cleared at once")
	}
	now = now.Add(msgTTL)
	m.Update(animMsg{})
	if m.msg != "" {
		t.Errorf("message %q outlived msgTTL", m.msg)
	}
}

func TestHelpOverlay(t *testing.T) {
	zone.NewGlobal()
	m := New(&fakeBE{}, "")
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 30})
	as, _ := (&fakeBE{}).Refresh()
	m.setAgents(as)
	if v := m.View(); !strings.Contains(v, "no ▶ pane · click a wall pane to pick one · ⏎ add/evict · Space preview · ? all keys") {
		t.Errorf("header hint under fleet up with no target:\n%s", v)
	}
	m.be = &fakeBE{noWall: true}
	if v := m.View(); !strings.Contains(v, "⏎ replace ▶ · a add · Space preview · Tab next waiting · x take off wall · ←↑↓→ select · g what's new · ? all keys") {
		t.Errorf("header hint without fleet up:\n%s", v)
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if h := m.header(); !strings.Contains(h, "? all keys") || ansi.StringWidth(h) > 100 {
		t.Errorf("narrow header must keep \"? all keys\": %q", h)
	}
	press := func(r string) tea.Cmd {
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r)})
		return cmd
	}
	press("?")
	v := m.View()
	if !strings.Contains(v, "hide this pane (the agent keeps") || strings.Contains(v, "╭") {
		t.Fatalf("overlay should replace tiles:\n%s", v)
	}
	if cmd := press("q"); cmd != nil {
		t.Fatal("q with the overlay open must not quit")
	}
	if m.help || strings.Contains(m.View(), "hide this pane (the agent keeps") {
		t.Fatal("q should close the overlay")
	}
	press("?")
	press("?")
	if m.help {
		t.Fatal("? should toggle the overlay closed")
	}
	press("?")
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.help {
		t.Fatal("esc should close the overlay")
	}
	if cmd := press("q"); cmd == nil {
		t.Fatal("q without the overlay quits")
	}
}

// TestHelpOverlayShortPane: the overview pane is ~14 rows, shorter than the
// help list. The overlay switches to two columns and never loses the top.
func TestHelpOverlayShortPane(t *testing.T) {
	zone.NewGlobal()
	m := New(&fakeBE{}, "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 14})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	v := ansi.Strip(m.View())
	lines := strings.Split(v, "\n")
	if len(lines) > 14 {
		t.Errorf("view is %d lines, pane is 14", len(lines))
	}
	// columns are half width, so the Alt-z description may wrap after "again to"
	for _, want := range []string{"Panes", "Admiral", "Alt-z         zoom this pane; press again", "Alt-w 1/2/4/6/9/m"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
	for _, l := range lines {
		if ansi.StringWidth(l) > 120 {
			t.Errorf("line wider than 120: %q", l)
		}
	}
	if !strings.Contains(v, "↓ more · ↑↓ scroll") {
		t.Errorf("a cut overlay must say more keys follow:\n%s", v)
	}
}

// TestHelpOverlayScrollsToEveryKey: at 120x14 the overlay scrolls (↓/j,
// PgDn, wheel) and every registry key is visible at some offset.
func TestHelpOverlayScrollsToEveryKey(t *testing.T) {
	zone.NewGlobal()
	m := New(&fakeBE{}, "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 14})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	seen := map[string]bool{}
	var all strings.Builder
	view := func() string {
		v := ansi.Strip(m.View())
		if n := len(strings.Split(v, "\n")); n > 14 {
			t.Fatalf("view is %d lines at offset %d, pane is 14", n, m.helpOff)
		}
		all.WriteString(v + "\n")
		return v
	}
	view()
	for i := 0; i < 100; i++ {
		prev := m.helpOff
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
		v := view()
		if m.helpOff == prev {
			if strings.Contains(v, "↓ more") || !strings.Contains(v, "↑ more · ↑↓ scroll") {
				t.Errorf("at the end the last row must say only ↑ more:\n%s", v)
			}
			break
		}
		if m.helpOff > 0 && !strings.Contains(v, "↑ more · ↓ more · ↑↓ scroll") && !strings.Contains(v, "↑ more · ↑↓ scroll") {
			t.Errorf("scrolled overlay must say ↑ more:\n%s", v)
		}
		seen[fmt.Sprint(m.helpOff)] = true
	}
	if len(seen) == 0 {
		t.Fatal("↓ did not scroll")
	}
	got := all.String()
	for _, b := range keys.Bindings {
		if !strings.Contains(got, b.Key) {
			t.Errorf("key %q never visible while scrolling", b.Key)
		}
	}
	if !strings.Contains(got, "Alt-m") {
		t.Error("Alt-m never visible")
	}
	end := m.helpOff
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if m.helpOff != end-1 {
		t.Errorf("wheel up: offset %d, want %d", m.helpOff, end-1)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if m.helpOff != 0 {
		t.Errorf("PgUp: offset %d, want 0 (clamped)", m.helpOff)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if m.helpOff == 0 {
		t.Error("PgDn/wheel down did not scroll")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.help || m.helpOff != 0 {
		t.Errorf("esc: help %v off %d, want closed and reset", m.help, m.helpOff)
	}
}

func TestSupCtxSetOnlyWhenChanged(t *testing.T) {
	be := &fakeBE{}
	m := New(be, "")
	tick := func() {
		msg := m.poll()().(agentsMsg)
		if cmd := m.syncSupCtx(msg.supCtx); cmd != nil {
			cmd()
		}
	}
	tick() // unknown: unset once
	tick()
	be.ctx = [2]string{"34%", "#a6e3a1"}
	tick()
	tick()
	be.ctx = [2]string{"", ""}
	tick()
	want := [][2]string{{"", ""}, {"34%", "#a6e3a1"}, {"", ""}}
	if len(be.ctxSets) != len(want) {
		t.Fatalf("sets %v", be.ctxSets)
	}
	for i := range want {
		if be.ctxSets[i] != want[i] {
			t.Errorf("set %d: %v", i, be.ctxSets[i])
		}
	}
}

func TestResetKey(t *testing.T) {
	be := &fakeBE{noWall: true}
	m := New(be, "")
	if cmd := m.key("R"); cmd != nil || m.msg != "R needs fleet up" {
		t.Fatalf("no wall: msg=%q", m.msg)
	}
	be.noWall = false
	m.msg = ""
	cmd := m.key("R")
	if cmd == nil {
		t.Fatal("R must ask tmux to confirm")
	}
	cmd()
	if be.resets != 1 || m.msg != "" {
		t.Errorf("resets=%d msg=%q", be.resets, m.msg)
	}
}

func TestChangesKey(t *testing.T) {
	be := &fakeBE{noWall: true}
	m := New(be, "")
	if cmd := m.key("g"); cmd != nil || m.msg != "g needs fleet up" {
		t.Fatalf("no wall: msg=%q", m.msg)
	}
	be.noWall = false
	m.msg = ""
	cmd := m.key("g")
	if cmd == nil {
		t.Fatal("g must open the what's new popup")
	}
	cmd()
	if be.changes != 1 || m.msg != "" {
		t.Errorf("changes=%d msg=%q", be.changes, m.msg)
	}
}

func TestTileShowsReportUnlessWorking(t *testing.T) {
	a := &board.Agent{Name: "x", Status: "idle", LastLine: "screen line", Report: &board.Report{State: "done", Summary: "merged feat/x"}}
	if got := ansi.Strip(line3(a, 40)); got != "✓ done: merged feat/x" {
		t.Fatalf("idle with report: %q", got)
	}
	a.Report.State, a.Status = "blocked", "waiting"
	if got := ansi.Strip(line3(a, 40)); got != "✗ blocked: merged feat/x" {
		t.Fatalf("waiting with report: %q", got)
	}
	a.Status = "working"
	if got := ansi.Strip(line3(a, 40)); got != "screen line" {
		t.Fatalf("working: %q", got)
	}
	a.Report, a.Status = nil, "idle"
	if got := ansi.Strip(line3(a, 40)); got != "screen line" {
		t.Fatalf("no report: %q", got)
	}
}

func TestTeamMark(t *testing.T) {
	zone.NewGlobal()
	now := time.Now()
	render := func(teams map[string]team.Info, a *board.Agent) (tile, chip string) {
		m := New(&fakeBE{}, "")
		m.now = func() time.Time { return now }
		m.Update(tea.WindowSizeMsg{Width: 80, Height: 9})
		m.teams = teams
		m.onWall = map[string]bool{a.Name: true}
		return m.tile(a, m.cfg.TileW), m.chips([]*board.Agent{a}, true)
	}
	a := &board.Agent{Name: "a-rather-long-agent-name", Profile: "copilot", Status: "idle", StatusSince: now.Add(-3 * time.Minute)}
	tile, chip := render(map[string]team.Info{a.Name: {Team: "frontend-squad", Color: "#89b4fa"}}, a)
	plainTile := ansi.Strip(tile)
	for _, want := range []string{"◆ ▣", "○ a-rather-long… ◆ ▣", "frontend-… · idle 3m"} {
		if !strings.Contains(plainTile, want) {
			t.Errorf("tile missing %q:\n%s", want, plainTile)
		}
	}
	if !strings.Contains(ansi.Strip(chip), "○ a-rather-long-agent-name ◆") {
		t.Errorf("chip %q", ansi.Strip(chip))
	}
	// Line 1 never wraps: the tile keeps its height.
	if got := strings.Count(tile, "\n"); got != layout.Default.TileH-1 {
		t.Errorf("tile has %d lines:\n%s", got+1, plainTile)
	}
	// No team: identical to an empty teams map (and to nil).
	t1, c1 := render(nil, a)
	t2, c2 := render(map[string]team.Info{"other": {Team: "x", Color: "#f5c2e7"}}, a)
	if t1 != t2 || c1 != c2 || strings.Contains(t1, "◆") || strings.Contains(c1, "◆") {
		t.Error("an agent without a team must render as before")
	}
}

func TestPickProfileKey(t *testing.T) {
	be := &fakeBE{noWall: true}
	m := New(be, "")
	if cmd := m.key("P"); cmd != nil || m.msg != "P needs fleet up" {
		t.Fatalf("no wall: msg=%q", m.msg)
	}
	be.noWall = false
	m.msg = ""
	cmd := m.key("P")
	if cmd == nil {
		t.Fatal("P must open the profile picker")
	}
	cmd()
	if be.picks != 1 || m.msg != "" {
		t.Errorf("picks=%d msg=%q", be.picks, m.msg)
	}
}

// Tab says what it did like a click does (replaced / added / already on the
// wall), and a refusal leaves the cursor and ▶ alone.
func TestNextMessages(t *testing.T) {
	be := &fakeBE{nextName: "delta", target: "bravo"}
	m := New(be, "")
	as, _ := be.Refresh()
	m.setAgents(as)
	m.target, m.sel = "bravo", "echo"
	if got := m.next(1)().(wallMsg); got.info != "delta → ▶ (was bravo) · u undo" {
		t.Errorf("replace: %q", got.info)
	}
	be.target = ""
	if got := m.next(1)().(wallMsg); got.info != "delta added to wall" {
		t.Errorf("append: %q", got.info)
	}
	be.nextName = "alpha" // fakeBE.OnWall has alpha
	if got := m.next(1)().(wallMsg); got.info != "alpha on wall" {
		t.Errorf("already there: %q", got.info)
	}
	be.nextName, be.nextRefuse = "delta", true
	m.Update(m.next(1)())
	if m.msg != wall.FullMsg || m.sel != "echo" || m.target != "bravo" {
		t.Errorf("refused: msg %q sel %q target %q", m.msg, m.sel, m.target)
	}
}

func TestTileACPMarker(t *testing.T) {
	zone.NewGlobal()
	now := time.Now()
	m := New(&fakeBE{}, "")
	m.now = func() time.Time { return now }
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 9})
	a := &board.Agent{Name: "a1", Profile: "copilot", Status: "idle", StatusSince: now.Add(-3 * time.Minute)}
	if tile := ansi.Strip(m.tile(a, m.cfg.TileW)); !strings.Contains(tile, "copilot · idle 3m") || strings.Contains(tile, "acp") {
		t.Errorf("tmux tile:\n%s", tile)
	}
	a.Driver = board.DriverACP
	if tile := ansi.Strip(m.tile(a, m.cfg.TileW)); !strings.Contains(tile, "copil…·acp · idle 3m") {
		t.Errorf("acp tile: the mark stays whole, the profile is cut:\n%s", tile)
	}
	if got := metaLineMark("copilot", "·acp", "idle 3m", 30); got != "copilot·acp · idle 3m" {
		t.Errorf("wide: %q", got)
	}
}

// Nothing on the overview blinks or animates (ADR-81): working, waiting and
// subagent tiles keep exactly the same frame across ticks, so bubbletea writes
// nothing and tmux never moves the cursor out of the admiral pane. No cell
// carries the SGR blink attribute either.
func TestOverviewNeverBlinks(t *testing.T) {
	now := time.Now()
	m := New(&fakeBE{}, "")
	m.now = func() time.Time { return now }
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	t0 := now.Add(-time.Minute)
	m.Update(agentsMsg{agents: []*board.Agent{
		{Name: "alpha", Profile: "fake", Status: "working", StatusSince: t0,
			Subagents: subs(status.SubRunning, status.SubRunning, status.SubDone)},
		{Name: "bravo", Profile: "fake", Status: "waiting", StatusSince: t0},
		{Name: "charlie", Profile: "fake", Status: "idle", StatusSince: t0,
			Report: &board.Report{State: "done", Summary: "shipped"}},
		{Name: "delta", Profile: "fake", Status: "error", StatusSince: t0},
	}})
	first := m.View()
	if !strings.Contains(ansi.Strip(first), "○ alpha") || !strings.Contains(ansi.Strip(first), "● bravo") {
		t.Fatalf("static glyphs missing:\n%s", ansi.Strip(first))
	}
	if sgrBlinks(first) {
		t.Error("view carries the SGR blink attribute")
	}
	for i := 0; i < 20; i++ {
		m.Update(animMsg{})
		if v := m.View(); v != first {
			t.Fatalf("tick %d changed the frame:\n%s\nwas:\n%s", i, v, first)
		}
	}
}

// sgrBlinks reports whether any SGR sequence in s sets blink (5 or 6),
// skipping the colour arguments of 38/48/58 (5;n and 2;r;g;b).
func sgrBlinks(s string) bool {
	for _, m := range regexp.MustCompile(`\x1b\[([0-9;:]*)m`).FindAllStringSubmatch(s, -1) {
		ps := strings.Split(m[1], ";")
		for i := 0; i < len(ps); i++ {
			switch ps[i] {
			case "38", "48", "58":
				if i+1 < len(ps) && ps[i+1] == "5" {
					i += 2
				} else if i+1 < len(ps) && ps[i+1] == "2" {
					i += 4
				}
			case "5", "6":
				return true
			}
		}
	}
	return false
}
