package mcp

// Instructions-changed hint (ADR-102): the server remembers a hash of the
// instructions it served at initialize, passes it across a reload in
// FLEET_MCP_SERVED, and tells the client once per change when the current
// text differs.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
)

const (
	envServed = "FLEET_MCP_SERVED"
	// instructionsChangedNote ends one tool result per session after the
	// instructions text changed.
	instructionsChangedNote = "\n\n[fleet] instructions changed since this session started; call fleet_instructions"
)

var instructionsToolDef = tool{"fleet_instructions", "Return the current fleet instructions for your role and their hash. Call it when a tool result says the instructions changed.", obj(nil, map[string]any{})}

// instructionsHash is a short hash of an instructions text.
func instructionsHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:12]
}

// rulesText is the instructions the client holds: what initialize sends, or
// for an admiral with the rules in its system prompt, those rules.
func (s *Server) rulesText() string {
	if s.Agent == "" && s.RulesInPrompt {
		return Instructions
	}
	return s.instructions()
}

// takeServedHash adopts the hash a reloaded process inherited and clears it
// from the environment so children don't inherit it.
func (s *Server) takeServedHash() {
	if v := os.Getenv(envServed); v != "" && s.Reloaded && s.served == "" {
		s.served = v
	}
	os.Unsetenv(envServed)
}

// instructionsNote returns the changed-instructions hint when the current
// text differs from the one the client last got. Giving the hint, or the
// text through fleet_instructions, makes the current text the served one,
// so each change is reported once, also across later reloads.
func (s *Server) instructionsNote(tool string) string {
	if s.served == "" {
		return ""
	}
	cur := instructionsHash(s.rulesText())
	if cur == s.served {
		return ""
	}
	s.served = cur
	if tool == "fleet_instructions" {
		return ""
	}
	return instructionsChangedNote
}

// currentInstructions is fleet_instructions' result.
func (s *Server) currentInstructions() string {
	text := s.rulesText()
	return fmt.Sprintf("instructions %s\n\n%s", instructionsHash(text), text)
}

func init() { toolDefs = append(toolDefs, instructionsToolDef) }
