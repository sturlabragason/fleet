package status

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StopEvent reports whether event ends the agent's turn: Claude's Stop or
// Copilot's agentStop.
func StopEvent(event string) bool {
	e := normEvent(event)
	return e == "stop" || e == "agentstop"
}

// StopBlockDir holds one file per agent: when fleet's Stop hook last blocked
// it, so it never blocks twice for one prompt (ADR-105).
func StopBlockDir(home string) string { return filepath.Join(StateDir(home), "stop") }

func stopBlockPath(home, agent string) string {
	return filepath.Join(StopBlockDir(home), agent)
}

// StopBlockedAt is when fleet last blocked agent's stop; zero when never.
// Any error but a missing file is returned, and the caller then allows.
func StopBlockedAt(home, agent string) (time.Time, error) {
	b, err := os.ReadFile(stopBlockPath(home, agent))
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
}

// MarkStopBlocked records that fleet blocked agent's stop at t.
func MarkStopBlocked(home, agent string, t time.Time) error {
	if err := os.MkdirAll(StopBlockDir(home), 0o755); err != nil {
		return err
	}
	tmp := stopBlockPath(home, agent) + ".tmp"
	if err := os.WriteFile(tmp, []byte(t.UTC().Format(time.RFC3339Nano)+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, stopBlockPath(home, agent))
}

// DropStopBlock removes agent's marker, so a new agent of the same name
// starts with none.
func DropStopBlock(home, agent string) {
	if agent != "" {
		_ = os.Remove(stopBlockPath(home, agent))
	}
}

// StopBlockJSON is the hook output that makes the tool take another turn.
// Claude's Stop and Copilot's agentStop read the same shape (S0 spike,
// ADR-105).
func StopBlockJSON(reason string) []byte {
	b, _ := json.Marshal(struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}{"block", reason})
	return append(b, '\n')
}
