package fleet

import (
	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/status"
)

// HoldForQueued appends text to a queued agent's first prompt, so it is
// delivered when the agent starts instead of being refused (fleet#33). It
// runs under the board lock, as the drain's claim does, so the text is
// either in the prompt the drain launches with or the agent is no longer
// queued and held is false.
func (f *Fleet) HoldForQueued(name, text string) (held bool, err error) {
	err = f.Store.Update(func(b *board.Board) error {
		a, ok := b.Agents[name]
		if !ok || a.Status != string(status.Queued) {
			return nil
		}
		if a.Goal != "" {
			a.Goal += "\n\n"
		}
		a.Goal += text
		held = true
		return nil
	})
	return held, err
}

// HeldNote is the reply for a send held for a queued agent.
func HeldNote(name string) string {
	return "queued for " + name + ": delivered when it starts"
}
