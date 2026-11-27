// Package schedule runs routes on a calendar (ADR-211). The calendar is
// stored on the route; a systemd --user timer per scheduled route runs
// `fleet schedule run --timer <route>`, which checks the gates, spawns the
// agent through the route and writes one line per firing to
// <fleet home>/schedule.jsonl. The agent name links the line to its run in
// runs.jsonl (ADR-170).
package schedule

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// LogName is the run log in the fleet home.
const LogName = "schedule.jsonl"

// Outcomes of one firing.
const (
	Started = "started"
	Skipped = "skipped"
	Failed  = "failed"
)

// Entry is one firing of a schedule.
type Entry struct {
	Route   string    `json:"route"`
	Due     time.Time `json:"due,omitzero"` // the calendar time it fired for; zero for a manual run
	At      time.Time `json:"at"`
	Outcome string    `json:"outcome"`
	Reason  string    `json:"reason,omitempty"`
	Agent   string    `json:"agent,omitempty"`
	Manual  bool      `json:"manual,omitempty"`
}

// Append writes e as one line at the end of the log.
func Append(home string, e Entry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode schedule entry %s: %w", e.Route, err)
	}
	f, err := os.OpenFile(filepath.Join(home, LogName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open schedule log: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("append schedule log: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close schedule log: %w", err)
	}
	return nil
}

// Read returns every entry, oldest first. A missing log is none; a line
// that does not parse is skipped.
func Read(home string) ([]Entry, error) {
	f, err := os.Open(filepath.Join(home, LogName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open schedule log: %w", err)
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Route != "" {
			out = append(out, e)
		}
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("read schedule log: %w", err)
	}
	return out, nil
}

// Last is each route's newest entry.
func Last(es []Entry) map[string]Entry {
	m := map[string]Entry{}
	for _, e := range es {
		m[e.Route] = e
	}
	return m
}

// LastStarted is the newest started entry of route.
func LastStarted(es []Entry, route string) (Entry, bool) {
	for _, e := range slices.Backward(es) {
		if e.Route == route && e.Outcome == Started {
			return e, true
		}
	}
	return Entry{}, false
}

// OpenAgents counts the scheduled agents still on the board.
func OpenAgents(es []Entry, onBoard func(string) bool) int {
	seen := map[string]bool{}
	for _, e := range es {
		if e.Outcome == Started && e.Agent != "" && !seen[e.Agent] && onBoard(e.Agent) {
			seen[e.Agent] = true
		}
	}
	return len(seen)
}
