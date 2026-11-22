package mcp

// Instructions-changed hint (ADR-102): the server remembers a hash of the
// instructions it served at initialize, passes it across a reload in
// FLEET_MCP_SERVED, and tells the client once per change when the current
// text differs.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
)

const (
	envServed = "FLEET_MCP_SERVED"
	// instructionsChangedNote ends one tool result per session after the
	// instructions text changed.
	instructionsChangedNote = "\n\n[fleet] instructions changed since this session started; call fleet_instructions"
)

var instructionsToolDef = tool{"fleet_instructions", "Return what changed in the fleet instructions for your role since the text you have, with the new hash; full=true returns the whole text. Call it when a tool result says the instructions changed.", obj(nil, map[string]any{
	"full": map[string]any{"type": "boolean", "description": "the whole text instead of the changed lines"}})}

// instructionsHash is a short hash of an instructions text.
func instructionsHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:12]
}

// rulesText is the instructions the client holds: what initialize sends, or
// for an admiral with the rules in its system prompt, those rules.
func (s *Server) rulesText() string {
	if s.Agent == "" && (s.RulesInPrompt || s.External) {
		return Instructions
	}
	return s.instructions()
}

// takeServedHash adopts the hash a reloaded process inherited and clears it
// from the environment so children don't inherit it.
func (s *Server) takeServedHash() {
	if v := os.Getenv(envServed); v != "" && s.Reloaded && s.served == "" {
		s.served, s.had = v, v
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

// currentInstructions is fleet_instructions' result: the lines that
// changed since the text the client has (ADR-195), or the whole text when
// full is set or that text is not on disk.
func (s *Server) currentInstructions(raw json.RawMessage) string {
	var a struct {
		Full bool `json:"full"`
	}
	_ = json.Unmarshal(raw, &a) // no or bad arguments: the default, a diff
	text := s.rulesText()
	cur := instructionsHash(text)
	old, err := os.ReadFile(s.instructionsFile(s.had))
	s.keepInstructions(text)
	had := s.had
	s.had = cur
	// An external admiral got only a pointer at initialize and calls this
	// at the start of each chat, so it always needs the whole text.
	if a.Full || s.External || err != nil || had == cur {
		return fmt.Sprintf("instructions %s\n\n%s", cur, text)
	}
	return fmt.Sprintf("instructions %s, changes since %s (full=true for the whole text):\n%s", cur, had, lineDiff(string(old), text))
}

// instructionsFile is where the text with hash h is kept for a later diff.
func (s *Server) instructionsFile(h string) string {
	if s.F == nil || h == "" {
		return ""
	}
	return filepath.Join(s.F.Home, "instructions", h+".md")
}

// keepInstructions saves text under its hash, once, so a client that holds
// it can later get only what changed, also across a reload.
func (s *Server) keepInstructions(text string) {
	p := s.instructionsFile(instructionsHash(text))
	if p == "" {
		return
	}
	if _, err := os.Stat(p); err == nil {
		return
	}
	pruneOld(filepath.Dir(p), keepInstructionsFor)
	if os.MkdirAll(filepath.Dir(p), 0o700) != nil {
		return
	}
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		logx.For("mcp").Debug("instructions not kept", "err", err)
	}
}

// keepInstructionsFor is how long a served instructions text is kept for
// diffs; a session older than that gets the full text.
const keepInstructionsFor = 30 * 24 * time.Hour

// pruneOld removes files in dir last changed more than age ago.
func pruneOld(dir string, age time.Duration) {
	es, _ := os.ReadDir(dir) // a missing dir has nothing to prune
	for _, e := range es {
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > age {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// lineDiff lists the lines of b not in a ("+ ") and of a not in b ("- "),
// blank lines skipped. Order follows each text; moved lines don't show.
func lineDiff(a, b string) string {
	count := func(t string) map[string]int {
		m := map[string]int{}
		for l := range strings.SplitSeq(t, "\n") {
			m[l]++
		}
		return m
	}
	ca, cb := count(a), count(b)
	var out strings.Builder
	for l := range strings.SplitSeq(b, "\n") {
		if strings.TrimSpace(l) != "" && ca[l] == 0 {
			out.WriteString("+ " + l + "\n")
		}
	}
	for l := range strings.SplitSeq(a, "\n") {
		if strings.TrimSpace(l) != "" && cb[l] == 0 {
			out.WriteString("- " + l + "\n")
		}
	}
	if out.Len() == 0 {
		return "(no line changed)\n"
	}
	return out.String()
}
