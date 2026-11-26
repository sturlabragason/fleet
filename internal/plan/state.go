package plan

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/sysx"
)

// Phase is where one task of a running plan stands.
type Phase string

const (
	Pending   Phase = "pending" // waits for its dependencies
	Write     Phase = "write"   // the writer works, or reworks after findings
	Reviewing Phase = "review"  // the reviewer reads the writer's branch
	Merge     Phase = "merge"   // review passed; the writer merges its branch
	Done      Phase = "done"
	Blocked   Phase = "blocked" // the admiral was told why
)

// TaskState is one task's progress.
type TaskState struct {
	Phase Phase `json:"phase"`
	// Round counts review rounds, from 1.
	Round int `json:"round,omitempty"`
	// Since is when the phase began: a report older than it belongs to an
	// earlier phase.
	Since time.Time `json:"since,omitzero"`
	Note  string    `json:"note,omitempty"`
}

// State is a plan on disk with its progress (ADR-4): a restart reads it
// back and carries on.
type State struct {
	Plan *Plan `json:"plan"`
	// Admiral owns the plan's agents and gets their reports.
	Admiral string                `json:"admiral,omitempty"`
	Created time.Time             `json:"created"`
	Tasks   map[string]*TaskState `json:"tasks"`
}

// New is the state of a plan that has not started.
func New(p *Plan, admiral string, now time.Time) *State {
	s := &State{Plan: p, Admiral: admiral, Created: now, Tasks: map[string]*TaskState{}}
	for _, t := range p.Tasks {
		s.Tasks[t.Name] = &TaskState{Phase: Pending}
	}
	return s
}

// Ready lists the pending tasks whose dependencies are all done.
func (s *State) Ready() []string {
	var out []string
	for _, t := range s.Plan.Tasks {
		if s.Tasks[t.Name].Phase != Pending {
			continue
		}
		if !slices.ContainsFunc(t.After, func(d string) bool { return s.Tasks[d].Phase != Done }) {
			out = append(out, t.Name)
		}
	}
	return out
}

// Finished is whether every task is done or blocked.
func (s *State) Finished() bool {
	for _, ts := range s.Tasks {
		if ts.Phase != Done && ts.Phase != Blocked {
			return false
		}
	}
	return true
}

// Summary is one line per task: name, phase, round and note.
func (s *State) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "plan %s:\n", s.Plan.Name)
	for _, t := range s.Plan.Tasks {
		ts := s.Tasks[t.Name]
		fmt.Fprintf(&b, "  %-20s %s", t.Name, ts.Phase)
		if t.Review != nil && ts.Round > 0 {
			fmt.Fprintf(&b, " (round %d/%d)", ts.Round, t.Review.MaxRounds)
		}
		if ts.Note != "" {
			fmt.Fprintf(&b, ": %s", ts.Note)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// Dir is where plans and their state live.
func Dir(home string) string { return filepath.Join(home, "plans") }

func statePath(home, name string) string { return filepath.Join(Dir(home), name+".json") }

// ErrNoPlan is Load of a plan that was never started.
var ErrNoPlan = errors.New("no such plan")

// Load reads a plan's state.
func Load(home, name string) (*State, error) {
	b, err := os.ReadFile(statePath(home, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("plan %s: %w", name, ErrNoPlan)
	}
	if err != nil {
		return nil, fmt.Errorf("read plan %s: %w", name, err)
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse plan %s: %w", name, err)
	}
	if s.Plan == nil || s.Tasks == nil {
		return nil, fmt.Errorf("plan %s: state file has no plan", name)
	}
	return &s, nil
}

// List names the plans with a state file.
func List(home string) []string {
	ms, _ := filepath.Glob(filepath.Join(Dir(home), "*.json"))
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, strings.TrimSuffix(filepath.Base(m), ".json"))
	}
	slices.Sort(out)
	return out
}

// Update runs fn on a plan's state under the plan's lock and writes the
// result; s is nil when the plan has no state yet, and fn returns the state
// to write (nil writes nothing).
func Update(home, name string, fn func(s *State) (*State, error)) error {
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		return fmt.Errorf("plans dir: %w", err)
	}
	lf, err := os.OpenFile(filepath.Join(Dir(home), name+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("plan lock: %w", err)
	}
	defer lf.Close()
	if err := sysx.Flock(lf, sysx.LockEx); err != nil {
		return fmt.Errorf("plan lock: %w", err)
	}
	defer sysx.Unlock(lf)
	cur, err := Load(home, name)
	if err != nil && !errors.Is(err, ErrNoPlan) {
		return err
	}
	next, err := fn(cur)
	if err != nil || next == nil {
		return err
	}
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("encode plan %s: %w", name, err)
	}
	tmp := statePath(home, name) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write plan %s: %w", name, err)
	}
	if err := os.Rename(tmp, statePath(home, name)); err != nil {
		return fmt.Errorf("write plan %s: %w", name, err)
	}
	return nil
}
