// Package runs keeps the run ledger (ADR-170): one line per closed agent in
// <fleet home>/runs.jsonl with the configuration that started it and how the
// run went, so a change to the playbook, a route, a model or a profile can be
// compared with what came before. Closed records in closed.json last a day;
// the ledger is what analysis reads.
package runs

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
)

// FileName is the ledger in the fleet home.
const FileName = "runs.jsonl"

// Path is the ledger under home.
func Path(home string) string { return filepath.Join(home, FileName) }

// Record is one run: one agent from start to close. It never holds prompt,
// report or screen text.
type Record struct {
	Name          string           `json:"name"`
	Profile       string           `json:"profile,omitempty"`
	Model         string           `json:"model,omitempty"`
	Effort        string           `json:"effort,omitempty"`
	Driver        string           `json:"driver,omitempty"`
	FleetVersion  string           `json:"fleet_version,omitempty"`
	Repo          string           `json:"repo,omitempty"` // base name of the repo or dir
	Branch        string           `json:"branch,omitempty"`
	Interactive   bool             `json:"interactive,omitempty"`
	Leader        bool             `json:"leader,omitempty"`
	Created       time.Time        `json:"created"`
	Closed        time.Time        `json:"closed"`
	FirstDone     time.Time        `json:"first_done,omitzero"`
	Config        *board.RunConfig `json:"config,omitempty"`
	Check         bool             `json:"check,omitempty"`         // the card had a done-check
	CheckResult   string           `json:"check_result,omitempty"`  // last verdict: pass or fail
	Reports       map[string]int   `json:"reports,omitempty"`       // reports by state
	Last          string           `json:"last,omitempty"`          // state of the last report
	DoneBounces   int              `json:"done_bounces,omitempty"`  // done reports refused over the run
	DoneRefusals  map[string]int   `json:"done_refusals,omitempty"` // refused dones by reason code (ADR-187)
	Interventions map[string]int   `json:"interventions,omitempty"` // by event kind
	Sends         map[string]int   `json:"sends,omitempty"`         // deliveries by mode, user_pane and takeover
	TimeS         map[string]int   `json:"time_s,omitempty"`        // seconds in each status
	Exit          string           `json:"exit,omitempty"`          // clean, crash or closed
	HasRetro      bool             `json:"has_retro,omitempty"`     // a report had a retro: line
	Spend         *Spend           `json:"spend,omitempty"`
}

// Spend is what the run's session cost, from internal/spend.
type Spend struct {
	Calls       int     `json:"calls,omitempty"`
	AIC         float64 `json:"aic,omitempty"`
	Tokens      int64   `json:"tokens,omitempty"`
	Compactions int     `json:"compactions,omitempty"`
}

// Done reports whether the run's last report was done.
func (r Record) Done() bool { return r.Last == "done" }

// Append writes rec as one line at the end of the ledger.
func Append(home string, rec Record) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode run %s: %w", rec.Name, err)
	}
	f, err := os.OpenFile(Path(home), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open run ledger: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("append run %s: %w", rec.Name, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close run ledger: %w", err)
	}
	return nil
}

// Read returns the runs closed at or after since, oldest first. A missing
// ledger is no runs; a line that does not parse is skipped.
func Read(home string, since time.Time) ([]Record, error) {
	f, err := os.Open(Path(home))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open run ledger: %w", err)
	}
	defer f.Close()
	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.Closed.Before(since) {
			continue
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("read run ledger: %w", err)
	}
	return out, nil
}

// Hash is the short content hash a fingerprint holds: 12 hex digits of
// sha256, "" for blank text.
func Hash(b []byte) string {
	if len(bytes.TrimSpace(b)) == 0 {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:6])
}

// AgentsMD hashes the agent notes in dir: AGENTS.md, else CLAUDE.md.
func AgentsMD(dir string) string {
	for _, n := range []string{"AGENTS.md", "CLAUDE.md"} {
		if b, err := os.ReadFile(filepath.Join(dir, n)); err == nil {
			return Hash(b)
		}
	}
	return ""
}
