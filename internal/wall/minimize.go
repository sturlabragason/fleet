package wall

import (
	"fmt"
	"slices"
	"strings"

	"github.com/sturlabragason/fleet/internal/logx"
)

// Minimize all (DESIGN.md ADR-67). HideAll takes every agent pane off the
// wall in one step, leaving the admiral and the overview; the agents keep
// running in their own windows. The pane ids that were on the wall are kept
// in the window option @fleet_minimized, in wall order, so Restore can bring
// them back.

// Messages for the minimize-all verbs.
const (
	NothingOnWallMsg     = "nothing on the wall"
	NothingToRestoreMsg  = "nothing to restore"
	minimizedOption      = "@fleet_minimized"
	minimizedRestoreHint = " · Alt-X restores"
)

// HideAll takes every agent pane off the wall, pinned ones too, and records
// them for Restore. An empty wall is a no-op that keeps the last record. It
// returns how many panes it took off.
func (w *Wall) HideAll() (int, error) {
	s, err := w.Read()
	if err != nil {
		return 0, err
	}
	if len(s.Wall) == 0 {
		return 0, nil
	}
	ids := make([]string, 0, len(s.Wall))
	b := w.newBatch(s)
	for _, p := range s.Wall {
		b.breakOut(p.ID)
		ids = append(ids, p.ID)
	}
	if t := w.opt("@fleet_target"); t != "" && slices.Contains(ids, t) {
		w.setOpt("@fleet_target", "") // a target off the wall is no target
	}
	w.setOpt(minimizedOption, strings.Join(ids, " "))
	s.Wall = nil
	_, err = b.commit(s)
	logx.For("wall").Info("hide all", "panes", len(ids))
	return len(ids), err
}

// Restore puts the panes the last HideAll took off back on the wall, in their
// old order, skipping panes that are gone, dead or already back, and stopping
// when the wall is full. The record is cleared. It returns how many came back.
func (w *Wall) Restore() (int, error) {
	rec := strings.Fields(w.opt(minimizedOption))
	if len(rec) == 0 {
		return 0, nil
	}
	w.setOpt(minimizedOption, "")
	s, err := w.Read()
	if err != nil {
		return 0, err
	}
	all, err := w.T.ListPanes("")
	if err != nil {
		return 0, err
	}
	session := strings.SplitN(w.Target, ":", 2)[0]
	slots := s.Slots()
	b := w.newBatch(s)
	var added []string
	for _, id := range rec {
		if len(s.Wall) >= slots {
			break
		}
		p, ok := paneByID(all, id)
		if !ok || p.Dead || p.Session != session || p.Agent == "" || onWall(s, id) {
			continue
		}
		target := s.Ov
		if len(s.Wall) > 0 {
			target = s.Wall[len(s.Wall)-1].ID
		}
		b.join(id, target)
		p.Role = "wall"
		s.Wall = append(s.Wall, p)
		added = append(added, id)
	}
	if len(added) == 0 {
		logx.For("wall").Info("restore", "panes", 0, "recorded", len(rec))
		return 0, nil
	}
	ok, err := b.commit(s)
	n := len(added)
	if !ok { // some joins may not have run: count what is on the wall
		n = 0
		if s, rerr := w.Read(); rerr == nil {
			for _, id := range added {
				if onWall(s, id) {
					n++
				}
			}
		}
	}
	logx.For("wall").Info("restore", "panes", n, "recorded", len(rec), "batched", ok)
	return n, err
}

// ToggleAll is the Alt-X key: Restore when the wall is empty and there is
// something to restore, otherwise HideAll. It returns the message to show.
func (w *Wall) ToggleAll() (string, error) {
	s, err := w.Read()
	if err != nil {
		return "", err
	}
	if len(s.Wall) == 0 {
		if w.opt(minimizedOption) == "" {
			return NothingOnWallMsg, nil
		}
		n, err := w.Restore()
		return RestoredMsg(n), err
	}
	n, err := w.HideAll()
	return HiddenAllMsg(n), err
}

// HiddenAllMsg says what HideAll did.
func HiddenAllMsg(n int) string {
	if n == 0 {
		return NothingOnWallMsg
	}
	return fmt.Sprintf("%s off the wall%s", panes(n), minimizedRestoreHint)
}

// RestoredMsg says what Restore did.
func RestoredMsg(n int) string {
	if n == 0 {
		return NothingToRestoreMsg
	}
	return fmt.Sprintf("%s back on the wall", panes(n))
}

func panes(n int) string {
	if n == 1 {
		return "1 pane"
	}
	return fmt.Sprintf("%d panes", n)
}
