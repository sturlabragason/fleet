package fleet

import (
	"strings"

	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/logx"
)

// Steer delivers text to a working agent now instead of after its turn
// (ADR-158) and says how, as an acphost Delivery value. An ACP agent's host
// steers it the way its tool allows (the profile's acp_steer). A tmux
// agent's own screen takes text typed mid-turn, so it just gets the send.
func (f *Fleet) Steer(name, text string) (string, error) {
	if !f.isACP(name) {
		if err := f.Send(name, text); err != nil {
			return "", err
		}
		return acphost.DeliverySent, nil
	}
	if _, err := f.Pane(name); err != nil {
		return "", err
	}
	how := f.Profiles[f.Store.Load().Agents[name].Profile].ACPSteer
	logx.For("fleet").Info("steer", "agent", name, "bytes", len(text), "how", how)
	resp, err := f.acpCall(name, acphost.Request{Op: acphost.OpSteer, Text: text, Steer: how})
	if err != nil {
		if strings.Contains(err.Error(), `unknown op "steer"`) {
			return f.steerOldHost(name, text, resp.Status)
		}
		return "", err
	}
	f.notePrompt(name)
	return resp.Delivery, nil
}

// steerOldHost steers through a host started before OpSteer existed: a
// working agent has its turn cancelled and gets the text with the note, an
// idle one an ordinary prompt. One waiting on a question has a turn in
// flight too, but cancelling would drop the question, so its text queues.
func (f *Fleet) steerOldHost(name, text, status string) (string, error) {
	if status != "working" {
		if _, err := f.acpSend(name, text); err != nil {
			return "", err
		}
		if status == "waiting" {
			return acphost.DeliveryQueued, nil
		}
		return acphost.DeliverySent, nil
	}
	if err := f.SendNow(name, acphost.InterruptNote+text); err != nil {
		return "", err
	}
	return acphost.DeliveryInterrupted, nil
}

// Queue sends text the ordinary way and says how: an ACP agent's host
// holds it while a turn runs (DeliveryQueued) and sends it when the turn
// ends (ADR-118).
func (f *Fleet) Queue(name, text string) (string, error) {
	if !f.isACP(name) {
		return acphost.DeliverySent, f.Send(name, text)
	}
	d, err := f.acpSend(name, text)
	if d == "" && err == nil {
		d = acphost.DeliverySent // a host from before ADR-158 does not say
	}
	return d, err
}

// DeliveryHeld: the agent waits in the spawn queue and gets the text when
// it starts (HoldForQueued).
const DeliveryHeld = "held"

// DeliveryNote tells the admiral or the user what a send did with the text
// (ADR-158): whether the agent sees it now or later, and what a steer
// stopped.
func DeliveryNote(name, d string) string {
	switch d {
	case DeliveryHeld:
		return HeldNote(name)
	case acphost.DeliverySteered:
		return "steered " + name + ": added to its running turn, nothing stopped"
	case acphost.DeliveryInterrupted:
		return "steered " + name + ": its tool takes no input mid-turn, so its turn was stopped and the text sent at once with a note saying so; " +
			"what it finished stays in its context, a step that was running may need redoing"
	case acphost.DeliveryQueued:
		return "queued for " + name + ": it gets the text when its current turn ends"
	}
	return "sent to " + name
}

// DeliveryWord is DeliveryNote in a word or two, for one line per target.
func DeliveryWord(d string) string {
	switch d {
	case DeliveryHeld:
		return "queued, delivered when it starts"
	case acphost.DeliverySteered:
		return "steered"
	case acphost.DeliveryInterrupted:
		return "steered (turn interrupted)"
	case acphost.DeliveryQueued:
		return "queued until its turn ends"
	}
	return "sent"
}
