package fleet

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/sessions"
	"github.com/sturlabragason/fleet/internal/status"
)

// Agents the user talks to directly (ADR-61, fleet#1). tmux tells fleet
// which pane each attached client has active and when the user last
// pressed a key there (client_activity); fleet's own paste and send-keys do
// not touch it, so a recent key press in an agent's pane is the user's.

// ClientSeen is one attached tmux client: its active pane and its last key
// press.
type ClientSeen struct {
	Pane   string
	Active time.Time
}

// clients lists the attached tmux clients; none when there is no server.
func (f *Fleet) clients() []ClientSeen {
	if f.Clients != nil {
		return f.Clients()
	}
	out, err := f.T.Run("list-clients", "-F", "#{pane_id}\t#{client_activity}")
	if err != nil {
		return nil
	}
	var cs []ClientSeen
	for l := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		pane, at, ok := strings.Cut(l, "\t")
		s, err := strconv.ParseInt(at, 10, 64)
		if !ok || err != nil || pane == "" {
			continue
		}
		cs = append(cs, ClientSeen{Pane: pane, Active: time.Unix(s, 0)})
	}
	return cs
}

// userOn reports whether a client has pane active, and the newest key
// press among those clients.
func userOn(cs []ClientSeen, pane string) (open bool, last time.Time) {
	for _, c := range cs {
		if pane != "" && c.Pane == pane {
			open = true
			if c.Active.After(last) {
				last = c.Active
			}
		}
	}
	return open, last
}

// userPaneGap is the quiet time before a key press in an agent's pane
// counts as a new user_pane deliver event (ADR-187), so typing is one event
// per burst, not one per poll.
const userPaneGap = time.Minute

// noteUser records the user's key presses in a's pane on the board and
// reports whether they start a new burst (userPaneGap). Called under the
// board lock by Refresh, with the clients read before it.
func noteUser(a *board.Agent, cs []ClientSeen, pane string) (burst bool) {
	if _, last := userOn(cs, pane); last.After(a.UserAt) && last.After(a.Created) {
		burst = last.Sub(a.UserAt) >= userPaneGap
		a.UserAt = last
	}
	return burst
}

// userBlocker returns why a done agent must stay open for the user ("" =
// it need not): flagged interactive, its pane open in a client, or typed in
// within board.UserHold. Every reason starts with "interactive".
func (f *Fleet) userBlocker(a *board.Agent) string {
	if a.Interactive {
		return "interactive"
	}
	open, last := userOn(f.clients(), f.paneOf(a.Name))
	if a.UserAt.After(last) {
		last = a.UserAt
	}
	switch {
	case open:
		return "interactive (the user has its pane open)"
	case !last.IsZero() && time.Since(last) < board.UserHold:
		return "interactive (the user typed in its pane " + sessions.Age(time.Since(last)) + " ago)"
	}
	return ""
}

// SendBlocker returns why typing into name now could land in the user's own
// input ("" = fleet may type): the user pressed a key in its pane within
// board.UserTyping, it is interactive and open in a client, or — when submit (the
// send pastes text or presses Enter) — its input box already holds text
// that is not fleet's own pending paste. fleet_send, `fleet send` and a team
// send refuse on it unless forced (ADR-61).
func (f *Fleet) SendBlocker(name string, submit bool) string {
	why, _ := f.SendBlock(name, submit)
	return why
}

// SendBlock is SendBlocker plus a short code for the reason (user_typing,
// interactive_open, draft), which the event log records instead of the
// reason's text (ADR-154).
func (f *Fleet) SendBlock(name string, submit bool) (why, code string) {
	a, ok := f.Store.Load().Agents[name]
	pane := f.paneOf(name)
	if !ok || pane == "" {
		return "", "" // Send itself says what is wrong
	}
	open, last := userOn(f.clients(), pane)
	if a.UserAt.After(last) {
		last = a.UserAt
	}
	switch {
	case !last.IsZero() && time.Since(last) < board.UserTyping:
		return fmt.Sprintf("the user typed in %s's pane %s ago", name, sessions.Age(time.Since(last))), "user_typing"
	case open && a.Interactive:
		return fmt.Sprintf("%s is interactive and the user has its pane open", name), "interactive_open"
	}
	if submit && !a.ACP() {
		if in := f.draftIn(name, pane); in != "" {
			return fmt.Sprintf("%s's input box holds unsubmitted text (%q)", name, status.Cut(in, 40)), "draft"
		}
	}
	return "", ""
}

// draftIn returns the user's unsubmitted text in agent name's input box ("" =
// empty or unreadable). A pending-paste marker is fleet's own unsubmitted
// send, which keys=["Enter"] is meant to submit.
func (f *Fleet) draftIn(name, pane string) string {
	set := f.setOf(name)
	screen, err := f.T.Capture(pane, 0)
	if err != nil {
		return ""
	}
	if _, ours := set.Unsubmitted(screen); ours {
		return ""
	}
	in, _ := set.InputBox(screen)
	return strings.TrimSpace(in)
}

// AdmiralDraft returns the text the user left unsubmitted in admiral name's
// input box ("" = none): what replacing the admiral would lose. A fleet wake
// line not yet submitted is fleet's own.
func (f *Fleet) AdmiralDraft(name string) string {
	in := strings.TrimSpace(f.admiralView(name).Input)
	if strings.HasPrefix(in, "[fleet]") {
		return ""
	}
	return in
}

// AdmiralBusy reports whether admiral name's pane shows it working: found,
// and not detected idle.
func (f *Fleet) AdmiralBusy(name string) bool {
	v := f.admiralView(name)
	return v.Pane != "" && !v.Idle
}

// RestoreAdmiralDraft types text, without Enter, into admiral name's input
// box once the box shows and is empty, polling every `every` for up to wait.
// It reports whether the text was typed.
func (f *Fleet) RestoreAdmiralDraft(name, text string, wait, every time.Duration) bool {
	end := time.Now().Add(wait)
	for {
		if v := f.admiralView(name); v.Pane != "" && v.Box && strings.TrimSpace(v.Input) == "" {
			return f.typeText(v.Pane, text) == nil
		}
		if time.Now().After(end) {
			return false
		}
		time.Sleep(every)
	}
}

// UserError is the refusal for a SendBlocker reason.
func UserError(reason string) error {
	logx.For("fleet").Info("send refused: user input", "reason", reason)
	return fmt.Errorf("not sent: %s. Typing now would merge into the user's input and submit it; wait, or ask the user, then send again with force=true", reason)
}
