package wall

import (
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
)

// Undo and pin (DESIGN.md §5e). show records every replace of the target
// slot in the window option @fleet_undo as "<incoming> <replaced> <name>":
// pane ids, then the replaced agent's name so a pane that is gone can still be
// named. Old two-field records are read too.

// NothingToUndoMsg is the answer when there is no replace to undo.
const NothingToUndoMsg = "nothing to undo"

// PinnedMsg is shown when a card would replace a pinned pane.
func PinnedMsg(name string) string { return name + " is pinned · p unpins" }

// ShownMsg says what a Show did, from the ▶ agent before it (before), whether
// the agent was already on the wall (wasOn) and whether before is still on the
// wall afterwards (beforeOn): it replaced the ▶ pane, was refused by a pinned
// ▶ pane and placed by the old rules, was appended, or was already there.
func ShownMsg(name, before string, wasOn, beforeOn bool) string {
	switch {
	case wasOn:
		return name + " on wall"
	case before != "" && before != name && !beforeOn:
		return name + " → ▶ (was " + before + ") · u undo"
	case before != "" && before != name:
		return PinnedMsg(before)
	}
	return name + " added to wall"
}

// Undo swaps the last replace back: the pane that left goes into the slot of
// the pane that came in, keeping focus, the target follows it and the reverse
// is recorded so a second Undo redoes. It returns the message to show and the
// agent now in the ▶ slot ("" when nothing was undone; the record is then
// cleared, unless the slot is pinned).
func (w *Wall) Undo() (msg, target string, err error) {
	in, out, outName := parseUndo(w.opt("@fleet_undo"))
	s, err := w.Read()
	if err != nil {
		return "", "", err
	}
	nothing := func(m string) (string, string, error) {
		w.setOpt("@fleet_undo", "")
		return m, "", nil
	}
	slot, ok := targetPane(s, in)
	if in == "" || out == "" || !ok {
		return nothing(NothingToUndoMsg)
	}
	if onWall(s, out) {
		return nothing(NothingToUndoMsg)
	}
	all, err := w.T.ListPanes("")
	if err != nil {
		return "", "", err
	}
	back, ok := paneByID(all, out)
	if !ok || back.Dead {
		name := outName
		if ok && back.Agent != "" {
			name = back.Agent
		}
		if name == "" {
			return nothing(NothingToUndoMsg)
		}
		return nothing(NothingToUndoMsg + " (" + name + " is gone)")
	}
	if slot.Pin {
		return PinnedMsg(slot.Agent), "", nil
	}
	now := strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := w.swapInto(out, in, now); err != nil {
		return "", "", err
	}
	w.setOpt("@fleet_undo", undoRecord(out, in, slot.Agent))
	logx.For("wall").Info("undo", "back", out, "was", in)
	return back.Agent + " back in ▶ (was " + slot.Agent + ") · u redo", back.Agent, nil
}

// TogglePin flips @fleet_pin on an agent pane, on or off the wall, and
// reports the new state. A pinned pane is never evicted or replaced (§5b.1
// class 0).
func (w *Wall) TogglePin(pane string) (bool, error) {
	v, err := w.T.Run("display-message", "-p", "-t", pane, "#{@fleet_pin}")
	if err != nil {
		return false, err
	}
	pinned := strings.TrimSpace(v) != "1"
	if pinned {
		err = w.T.SetPaneOption(pane, "@fleet_pin", "1")
	} else {
		_, err = w.T.Run("set-option", "-p", "-u", "-t", pane, "@fleet_pin")
	}
	if err != nil {
		return false, err
	}
	logx.For("wall").Info("pin", "pane", pane, "pinned", pinned)
	return pinned, w.ApplyCached() // the "next out" mark may move
}

// Pinned returns the names of the session's pinned agents, on the wall or not.
func (w *Wall) Pinned() map[string]bool {
	out := map[string]bool{}
	panes, err := w.listPanes("")
	if err != nil {
		return out
	}
	session := strings.SplitN(w.Target, ":", 2)[0]
	for _, p := range panes {
		if p.Session == session && p.Agent != "" && p.Pin {
			out[p.Agent] = true
		}
	}
	return out
}

// undoRecord is the @fleet_undo value for a replace of out by in; outName is
// the agent in out ("" when unknown).
func undoRecord(in, out, outName string) string {
	return strings.TrimSpace(in + " " + out + " " + outName)
}

// parseUndo reads @fleet_undo; a two-field record (before the name was kept)
// has no name.
func parseUndo(v string) (in, out, outName string) {
	f := strings.Fields(v)
	if len(f) < 2 {
		return "", "", ""
	}
	if len(f) > 2 {
		outName = f[2]
	}
	return f[0], f[1], outName
}
