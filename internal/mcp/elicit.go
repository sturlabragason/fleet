package mcp

// Form-mode elicitation for the confirmation gate (ADR-213). A client that
// declared capabilities.elicitation gets a server-to-client
// elicitation/create during the tools/call, and the user's answer in that
// dialog is the gate: the model cannot pre-approve with confirmed=true. A
// client that did not declare it never gets one (the spec: MUST NOT) and
// keeps confirmed=true.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
)

// dialogWait is how long fleet waits for the user to answer a dialog; then
// the call is refused and the dialog withdrawn.
const dialogWait = 10 * time.Minute

// elicits reports whether the client declared form-mode elicitation:
// 2025-06-18 `elicitation: {}`; 2025-11-25 `elicitation.form`, with an
// empty object still meaning form and `{url}` alone meaning no form.
func (s *Server) elicits() bool {
	var c struct {
		Elicitation *map[string]json.RawMessage `json:"elicitation"`
	}
	if json.Unmarshal(s.client.Capabilities, &c) != nil || c.Elicitation == nil {
		return false
	}
	form, ok := (*c.Elicitation)["form"]
	return len(*c.Elicitation) == 0 || ok && strings.HasPrefix(strings.TrimSpace(string(form)), "{")
}

// dialogHint rewrites a refusal's "ask the user, then call again with
// confirmed=true" for a client with fleet's dialog, so the model does not
// ask in chat before the dialog asks again.
func (s *Server) dialogHint(text string) string {
	if !s.elicits() {
		return text
	}
	return strings.Replace(text, fleet.AskThenConfirm, "call again with confirmed=true only if the user wants it answered; fleet then asks the user in a dialog", 1)
}

// dialogText is what the dialog asks: the action, then any warning.
func (g gate) dialogText() string {
	ask := g.ask
	if ask == "" {
		ask = "Allow " + g.tool + "?"
	}
	if w := strings.TrimSpace(strings.TrimPrefix(g.warn, ". ")); w != "" {
		ask += " " + w + "."
	}
	return ask
}

// elicit asks the user in the client's dialog and returns nil only on an
// accepted yes. It runs on the serving goroutine inside a tools/call: the
// reader goroutine (cancel.go) routes the client's response here, holds any
// other request for after this call, and a cancel of the call ends the wait.
func (s *Server) elicit(g gate) error {
	if s.out == nil || s.in == nil {
		return errors.New("not confirmed: fleet could not show its dialog here")
	}
	ctx, stop := s.callCtx(false)
	defer stop()
	if errors.Is(context.Cause(ctx), errCancelled) { // cancelled while it was queued: ask nobody
		return errCancelled
	}
	id := json.RawMessage(strconv.Quote("fleet-elicit-" + strconv.FormatInt(time.Now().UnixNano(), 36)))
	reply := s.await(id)
	defer s.unawait(id)
	if err := s.out.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": "elicitation/create", "params": map[string]any{
		"message": g.dialogText(),
		"requestedSchema": map[string]any{"type": "object", "required": []string{"confirm"}, "properties": map[string]any{
			"confirm": map[string]any{"type": "boolean", "title": "Yes, go ahead", "default": false}}},
	}}); err != nil {
		return fmt.Errorf("not confirmed: fleet's dialog could not be sent: %w", err)
	}
	wait := s.dialogWait
	if wait == 0 {
		wait = dialogWait
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	// Progress keeps a client's tool-call timeout (Copilot's 180 s, wait.go)
	// from ending the call while the user reads the dialog.
	tick := time.NewTicker(progressEvery)
	defer tick.Stop()
	for n := 1; ; n++ {
		select {
		case raw := <-reply:
			return dialogAnswer(raw)
		case <-ctx.Done():
			s.withdraw(id, "the tool call ended")
			if errors.Is(context.Cause(ctx), errCancelled) {
				return errCancelled // no reply goes out
			}
			return errors.New("not confirmed: the client went away before the user answered fleet's dialog")
		case <-t.C:
			s.withdraw(id, "no answer in time")
			return fmt.Errorf("not confirmed: nobody answered fleet's dialog within %s; call again when the user is back", wait)
		case <-tick.C:
			if s.progress != nil {
				s.progress(n, "waiting for the user's answer in fleet's dialog")
			}
		}
	}
}

// dialogAnswer reads the client's elicitation/create response.
func dialogAnswer(raw []byte) error {
	var r struct {
		Result struct {
			Action  string `json:"action"`
			Content struct {
				Confirm bool `json:"confirm"`
			} `json:"content"`
		} `json:"result"`
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("not confirmed: unreadable answer to fleet's dialog: %w", err)
	}
	switch {
	case r.Error != nil:
		return fmt.Errorf("not confirmed: the client could not show fleet's dialog: %s", r.Error.Message)
	case r.Result.Action == "accept" && r.Result.Content.Confirm:
		return nil
	case r.Result.Action == "accept", r.Result.Action == "decline":
		return errors.New("not confirmed: the user said no in fleet's dialog; don't call again unless the user asks for it")
	}
	return errors.New("not confirmed: the user closed fleet's dialog without answering; call again only if the user asks for it")
}

// withdraw tells the client fleet no longer waits for its request id, so
// it can close the dialog.
func (s *Server) withdraw(id json.RawMessage, reason string) {
	if err := s.out.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled",
		"params": map[string]any{"requestId": id, "reason": reason}}); err != nil {
		logx.For("mcp").Debug("dialog not withdrawn", "err", err)
	}
}
