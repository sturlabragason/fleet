package acp

import (
	"context"
	"encoding/json"
)

// MethodSteer adds a message to the running turn instead of starting a new
// one: the ACP steering extension claude-agent-acp serves (0.103.0, probed
// 2026-11-17). Copilot 1.0.112 and agy 1.5.9 do not advertise it (ADR-158).
const MethodSteer = "_session/steering"

// Steer outcomes.
const (
	// SteerInjected: the message joined the running turn; its output comes
	// as session/update and the turn's session/prompt answers when it is done.
	SteerInjected = "injected"
	// SteerPromptRequired: no turn was running, nothing was sent; send the
	// text as an ordinary session/prompt.
	SteerPromptRequired = "promptRequired"
)

// Steering reports whether the agent advertised MethodSteer: top-level
// initialize _meta {"steering": {"supported": true}}.
func (r InitializeResult) Steering() bool {
	var m struct {
		Steering struct {
			Supported bool `json:"supported"`
		} `json:"steering"`
	}
	_ = json.Unmarshal(r.Meta, &m) // a _meta fleet can't read advertises nothing
	return m.Steering.Supported
}

type steerParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
	Meta      map[string]any `json:"_meta"`
}

// Steer sends blocks into the running turn and returns the agent's
// outcome. It opts in to SteerPromptRequired, so an idle agent never starts
// a turn the caller does not track.
func (cl *Client) Steer(ctx context.Context, id string, blocks []ContentBlock) (string, error) {
	p := steerParams{SessionID: id, Prompt: blocks, Meta: map[string]any{
		"steering": map[string]any{"idleBehavior": "promptRequired"},
	}}
	var res struct {
		Outcome string `json:"outcome"`
	}
	if err := cl.c.call(ctx, MethodSteer, p, &res); err != nil {
		return "", err
	}
	return res.Outcome, nil
}
