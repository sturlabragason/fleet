package acphost

import (
	"errors"

	"github.com/sturlabragason/fleet/internal/acp"
	"github.com/sturlabragason/fleet/internal/logx"
)

// steer delivers text to the running turn now (OpSteer, ADR-158) and says
// how: the agent's own steering when it advertises acp.MethodSteer, a
// second session/prompt when how is SteerPrompt (the tool steers with
// it), else the turn is cancelled and text sent at once after
// InterruptNote. An idle agent just gets the prompt; one whose session is
// not ready yet gets it when it is.
func (h *host) steer(text, how string) (string, error) {
	h.mu.Lock()
	if h.stopping || h.exited {
		h.mu.Unlock()
		return "", errors.New("agent is stopping")
	}
	text = stripMarks(text)
	switch {
	case !h.ready:
		h.pending = append(h.pending, text)
		h.mu.Unlock()
		return DeliveryQueued, nil
	case h.inflight == 0:
		h.sendBy(text, "")
		h.mu.Unlock()
		return DeliverySent, nil
	case !h.steering && how == SteerPrompt:
		h.sendBy(text, "")
		h.mu.Unlock()
		return DeliverySteered, nil
	}
	ext, c, sid, pc := h.steering, h.conn, h.sid, h.caps.PromptCapabilities
	h.mu.Unlock()
	if ext {
		out, err := c.Steer(h.bg, sid, blocks(text, pc, func(s string) { h.say("! " + s) }))
		switch {
		case err == nil && out == acp.SteerInjected:
			h.mu.Lock()
			h.flush()
			h.emit(Entry{Kind: KindUser, Text: shown(text) + "  (steered into the running turn)"}, true)
			h.mu.Unlock()
			return DeliverySteered, nil
		case err == nil && out == acp.SteerPromptRequired:
			// The turn ended before the steer reached it.
			return h.prompt(text, "")
		}
		logx.For("acphost").Warn("steer failed; cancelling the turn instead", "agent", h.cfg.Name, "outcome", out, "err", err)
	}
	if err := h.sendNow(InterruptNote+text, ""); err != nil {
		return "", err
	}
	return DeliveryInterrupted, nil
}
