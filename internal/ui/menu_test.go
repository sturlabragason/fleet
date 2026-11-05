package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/sturlabragason/fleet/internal/wall"
)

// u, p and Space call the backend (DESIGN.md §5e) and say what they did.
func TestUndoPinPreviewKeys(t *testing.T) {
	be := &fakeBE{undoMsg: "bravo back in ▶ (was alpha) · u redo", undoTarget: "bravo"}
	m := New(be, "")
	as, _ := be.Refresh()
	m.setAgents(as)
	m.sel = "charlie"
	m.Update(m.key("u")())
	if be.undos != 1 || m.msg != be.undoMsg || m.target != "bravo" || m.sel != "bravo" {
		t.Errorf("undo: undos=%d msg=%q target=%q sel=%q", be.undos, m.msg, m.target, m.sel)
	}
	be.undoMsg, be.undoTarget = wall.NothingToUndoMsg, ""
	m.Update(m.key("u")())
	if m.msg != wall.NothingToUndoMsg || m.target != "bravo" {
		t.Errorf("nothing to undo: msg=%q target=%q", m.msg, m.target)
	}
	m.Update(m.key("p")())
	if len(be.pins) != 1 || be.pins[0] != "bravo" || m.msg != "bravo pinned ⚑" {
		t.Errorf("pin: %v %q", be.pins, m.msg)
	}
	m.Update(m.key("p")())
	if m.msg != "bravo unpinned" {
		t.Errorf("unpin: %q", m.msg)
	}
	if cmd := m.key(" "); cmd == nil || cmd() != nil {
		t.Fatal("Space must open the preview quietly")
	}
	if len(be.previews) != 1 || be.previews[0] != "bravo" {
		t.Errorf("previews %v", be.previews)
	}
	be.noWall = true
	for _, k := range []string{"u", "p", " "} {
		if m.key(k) != nil {
			t.Errorf("%q without fleet up must do nothing", k)
		}
	}
}

// A card that could not replace the ▶ pane because it is pinned says so, and
// the cursor stays on the pinned agent so p unpins it (not the new one).
func TestShowReportsAPinnedTarget(t *testing.T) {
	be := &fakeBE{target: "alpha"} // alpha stays on the wall
	m := New(be, "")
	as, _ := be.Refresh()
	m.setAgents(as)
	m.sel = "bravo"
	got := m.show()().(wallMsg)
	if got.info != "alpha is pinned · p unpins" {
		t.Errorf("got %q", got.info)
	}
	m.Update(got)
	if m.sel != "alpha" || m.target != "bravo" {
		t.Errorf("sel=%q target=%q, want the cursor on the pinned alpha", m.sel, m.target)
	}
	m.Update(m.key("p")())
	if len(be.pins) != 1 || be.pins[0] != "alpha" {
		t.Errorf("p pinned %v, want alpha", be.pins)
	}
}

// A right-click on a card opens the menu for that agent at the pointer on
// the button's release (a menu opened on the press would be closed by the
// release); a left click still shows it.
func TestRightClickOpensTheMenu(t *testing.T) {
	zone.NewGlobal()
	be := &fakeBE{}
	m := New(be, "")
	m.Update(tea.WindowSizeMsg{Width: 130, Height: 14})
	as, _ := be.Refresh()
	m.setAgents(as)
	m.View()
	time.Sleep(50 * time.Millisecond) // zones register asynchronously
	if _, cmd := m.Update(tea.MouseMsg{X: 50, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonRight}); cmd != nil {
		t.Fatal("right press must not open the menu")
	}
	_, cmd := m.Update(tea.MouseMsg{X: 50, Y: 3, Action: tea.MouseActionRelease, Button: tea.MouseButtonRight})
	if m.sel != "bravo" || cmd == nil {
		t.Fatalf("sel=%s cmd=%v", m.sel, cmd)
	}
	cmd()
	if len(be.menus) != 1 || be.menus[0] != "bravo 50,3" || len(be.shown) != 0 {
		t.Errorf("menus %v shown %v", be.menus, be.shown)
	}
	// X10 mouse reports the release as button none.
	m.Update(tea.MouseMsg{X: 50, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	if _, cmd := m.Update(tea.MouseMsg{X: 50, Y: 3, Action: tea.MouseActionRelease, Button: tea.MouseButtonNone}); cmd == nil {
		t.Fatal("release (none) after a right press must open the menu")
	}
	if _, cmd := m.Update(tea.MouseMsg{X: 50, Y: 3, Action: tea.MouseActionRelease, Button: tea.MouseButtonNone}); cmd != nil {
		t.Error("a stray release must do nothing")
	}
}

// A pinned agent's card and chip carry ⚑ after the name, one cell wide.
func TestPinMarkOnCardAndChip(t *testing.T) {
	if w := lipgloss.Width(pinMark); w != 1 {
		t.Fatalf("pin mark is %d cells", w)
	}
	be := &fakeBE{pinned: map[string]bool{"bravo": true}}
	m := New(be, "")
	m.pinned = be.pinned
	as, _ := be.Refresh()
	m.setAgents(as)
	m.target = "bravo"
	tile := ansi.Strip(m.tile(as[1], 40))
	if !strings.Contains(tile, "bravo ⚑ ▶") {
		t.Errorf("card:\n%s", tile)
	}
	if strings.Contains(ansi.Strip(m.tile(as[0], 40)), pinMark) {
		t.Error("unpinned card shows the pin")
	}
	m.w = 200
	if c := ansi.Strip(m.chips(as, true)); !strings.Contains(c, "bravo ⚑ ▶") {
		t.Errorf("chips: %q", c)
	}
}
