package mcp

// The confirmation dialog for 2026-07-28 clients (ADR-213, ADR-215): a
// stdio server sends no requests, so fleet answers the tools/call with
// resultType input_required and one form elicitation (multi round-trip
// requests, SEP-2322), and the client retries the call with the user's
// answer. The answer counts only with the requestState fleet signed for this
// exact call: an HMAC with a key that never leaves the process, over the
// admiral, the tool, its arguments, the dialog text, an expiry and a nonce
// spent on first use. The model sees neither the key nor the state.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// errInputRequired ends a tools/call with the call's input_required result.
var errInputRequired = errors.New("input required")

// mrtrKey signs requestState; per process, so a retry that reaches a
// reloaded process fails to verify and the model calls again.
var mrtrKey = func() []byte {
	k := make([]byte, 32)
	_, _ = rand.Read(k) // crypto/rand.Read never returns an error (Go 1.24+)
	return k
}()

// confirmKey is the inputRequests key of fleet's one question.
const confirmKey = "confirm"

// mrtrState is what requestState carries, signed.
type mrtrState struct {
	Admiral string `json:"a"`
	Tool    string `json:"t"`
	Args    string `json:"g"` // sha256 of the canonical arguments
	Ask     string `json:"q"` // sha256 of the dialog text
	Exp     int64  `json:"e"` // unix seconds
	Nonce   string `json:"n"`
}

// mrtrIn is a modern tools/call's MRTR fields and what the state binds.
type mrtrIn struct {
	tool      string
	args      json.RawMessage
	responses map[string]json.RawMessage
	state     string
	result    map[string]any // the input_required result, once confirm asks
}

// digest is a hex sha256 of canonical JSON (object keys sorted), so a
// client that re-encodes the arguments on retry still matches.
func digest(raw []byte) string {
	var v any
	if json.Unmarshal(raw, &v) == nil {
		if b, err := json.Marshal(v); err == nil {
			raw = b
		}
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func sign(body []byte) string {
	m := hmac.New(sha256.New, mrtrKey)
	m.Write(body)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// stateFor is this call's state for gate g.
func (s *Server) stateFor(in *mrtrIn, g gate, now time.Time) mrtrState {
	return mrtrState{Admiral: s.Admiral, Tool: in.tool, Args: digest(in.args), Ask: fmt.Sprintf("%x", sha256.Sum256([]byte(g.dialogText()))),
		Exp: now.Add(s.dialogTTL()).Unix()}
}

// mrtrConfirm is the modern dialog: nil on the user's yes in a retry that
// carries fleet's state for this call, errInputRequired to ask, else the
// refusal. It runs under s.mu (callTool), which guards spent.
func (s *Server) mrtrConfirm(g gate) error {
	if s.cur == nil || s.cur.mrtr == nil {
		return errors.New("not confirmed: fleet could not show its dialog here")
	}
	in, now := s.cur.mrtr, time.Now()
	answer, hasAnswer := in.responses[confirmKey]
	if in.state == "" || !hasAnswer {
		return s.askMRTR(in, g, now)
	}
	if err := s.verifyState(in, g, now); err != nil {
		return err
	}
	return elicitAnswer(answer)
}

// askMRTR fills the call's input_required result: the legacy dialog's
// question and schema, and a fresh signed state.
func (s *Server) askMRTR(in *mrtrIn, g gate, now time.Time) error {
	st := s.stateFor(in, g, now)
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce) // never fails, see mrtrKey
	st.Nonce = base64.RawURLEncoding.EncodeToString(nonce)
	body, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("not confirmed: fleet's dialog state: %w", err)
	}
	in.result = map[string]any{
		"resultType": "input_required",
		"inputRequests": map[string]any{confirmKey: map[string]any{"method": "elicitation/create", "params": map[string]any{
			"mode": "form", "message": g.dialogText(), "requestedSchema": confirmSchema}}},
		"requestState": base64.RawURLEncoding.EncodeToString(body) + "." + sign(body),
	}
	return errInputRequired
}

// verifyState refuses a state that is not fleet's (the spec: requestState
// is attacker-controlled), belongs to another call, expired, or was spent.
func (s *Server) verifyState(in *mrtrIn, g gate, now time.Time) error {
	b64, mac, ok := strings.Cut(in.state, ".")
	body, err := base64.RawURLEncoding.DecodeString(b64)
	var st mrtrState
	if !ok || err != nil || !hmac.Equal([]byte(mac), []byte(sign(body))) || json.Unmarshal(body, &st) != nil {
		return errors.New("not confirmed: the dialog answer did not come with fleet's state for it (fleet may have restarted); call again to ask the user")
	}
	want := s.stateFor(in, g, now)
	if st.Admiral != want.Admiral || st.Tool != want.Tool || st.Args != want.Args || st.Ask != want.Ask {
		return errors.New("not confirmed: the dialog answer was for a different call; call again to ask the user")
	}
	if now.Unix() > st.Exp {
		return errors.New("not confirmed: the dialog answer came too late; call again to ask the user")
	}
	if s.spent == nil {
		s.spent = map[string]int64{}
	}
	for n, exp := range s.spent {
		if now.Unix() > exp {
			delete(s.spent, n)
		}
	}
	if _, used := s.spent[st.Nonce]; used {
		return errors.New("not confirmed: that dialog answer was already used; call again to ask the user")
	}
	s.spent[st.Nonce] = st.Exp
	return nil
}
