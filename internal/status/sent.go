package status

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A tool's prompt hook (UserPromptSubmit, userPromptSubmitted) fires for
// every prompt, fleet's own included. fleet marks each text it types into a
// pane, so the hook can tell what the user typed himself (ADR-164).

const (
	sentKeep  = 8                // marks kept per agent
	sentMatch = 10 * time.Minute // a marked text matches a prompt this long
	sentAny   = 10 * time.Second // any prompt this soon after a mark is fleet's
	sentHead  = 60               // runes compared
)

type sentMark struct {
	At   time.Time `json:"at"`
	Head string    `json:"head"`
}

func sentPath(home, agent string) string {
	return filepath.Join(StateDir(home), agent+".sent.json")
}

// head is text with its whitespace squeezed, cut to sentHead runes.
func head(text string) string {
	s := strings.Join(strings.Fields(text), " ")
	if r := []rune(s); len(r) > sentHead {
		s = string(r[:sentHead])
	}
	return s
}

func readMarks(home, agent string) ([]sentMark, error) {
	b, err := os.ReadFile(sentPath(home, agent))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read sent marks: %w", err)
	}
	var ms []sentMark
	if json.Unmarshal(b, &ms) != nil {
		ms = nil // a torn file is no marks: the hook then records the prompt
	}
	return ms, nil
}

// MarkSent records that fleet is about to type text into agent's pane.
func MarkSent(home, agent, text string, now time.Time) error {
	ms, err := readMarks(home, agent)
	if err != nil {
		return err
	}
	ms = append(ms, sentMark{At: now, Head: head(text)})
	if len(ms) > sentKeep {
		ms = ms[len(ms)-sentKeep:]
	}
	b, err := json.Marshal(ms)
	if err != nil {
		return fmt.Errorf("encode sent marks: %w", err)
	}
	if err := os.MkdirAll(StateDir(home), 0o755); err != nil {
		return fmt.Errorf("sent marks dir: %w", err)
	}
	if err := os.WriteFile(sentPath(home, agent), b, 0o600); err != nil {
		return fmt.Errorf("write sent marks: %w", err)
	}
	return nil
}

// FleetSent is whether prompt is one fleet typed: its text was marked in
// the last sentMatch, or fleet typed anything in the last sentAny. A
// prompt starting "[fleet]" is always fleet's.
func FleetSent(home, agent, prompt string, now time.Time) bool {
	h := head(prompt)
	if strings.HasPrefix(h, "[fleet]") {
		return true
	}
	ms, err := readMarks(home, agent)
	if err != nil {
		return true // unreadable: never record a prompt that may be fleet's
	}
	for _, m := range ms {
		age := now.Sub(m.At)
		if age >= 0 && (age < sentAny || age < sentMatch && m.Head != "" && strings.HasPrefix(h, m.Head)) {
			return true
		}
	}
	return false
}

// ForgetSent drops agent's marks (at close).
func ForgetSent(home, agent string) {
	_ = os.Remove(sentPath(home, agent)) // gone already is fine
}
