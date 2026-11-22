// Package burn implements quota-aware agent spawning before subscription
// resets (ADR-190). It reads per-harness remaining quota, sizes how many runs
// fit above a reserve using runs.jsonl history, picks pending +burn tasks from
// the task CLI companion, and either logs the plan (phase 1 dry-run default) or
// spawns worktree-only agents and asks running ones to wrap up before reset.
package burn

import (
	"context"
	"time"

	"github.com/sturlabragason/fleet/internal/prefs"
)

// Window is one quota window reported by a harness (for example, a weekly
// limit or a 5-hour rolling window).
type Window struct {
	Group           string    `json:"group,omitempty"`
	Kind            string    `json:"kind"`                  // "weekly", "5h", "daily", ...
	Label           string    `json:"label,omitempty"`       // raw label from the tool
	RemainingPct    float64   `json:"remaining_pct"`         // 0..100
	UsedTokens      int64     `json:"used_tokens,omitempty"` // token-denominated quotas
	RemainingTokens int64     `json:"remaining_tokens,omitempty"`
	LimitTokens     int64     `json:"limit_tokens,omitempty"`
	ResetAt         time.Time `json:"reset_at,omitzero"`
}

// Quota is the snapshot returned by a reader for one configured profile.
type Quota struct {
	Profile   string    `json:"profile"`
	Reader    string    `json:"reader"`
	Available bool      `json:"available"`
	OverageOn bool      `json:"overage_on,omitempty"`
	Error     string    `json:"error,omitempty"`
	Hint      string    `json:"hint,omitempty"`
	Windows   []Window  `json:"windows,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
}

// PrimaryWindow selects the window matching kind (default "weekly") with the
// soonest reset time. If kind is empty or not found, it falls back to the
// first window with a non-zero ResetAt, else the first window.
func (q Quota) PrimaryWindow(kind string) (Window, bool) {
	if len(q.Windows) == 0 {
		return Window{}, false
	}
	if kind == "" {
		kind = "weekly"
	}
	var best Window
	found := false
	for _, w := range q.Windows {
		if w.Kind != kind {
			continue
		}
		if !found || (!w.ResetAt.IsZero() && (best.ResetAt.IsZero() || w.ResetAt.Before(best.ResetAt))) {
			best = w
			found = true
		}
	}
	if found {
		return best, true
	}
	for _, w := range q.Windows {
		if !w.ResetAt.IsZero() {
			return w, true
		}
	}
	return q.Windows[0], true
}

// Reader fetches a Quota snapshot for one profile.
type Reader interface {
	Name() string
	Read(ctx context.Context, profile string, cfg prefs.BurnProfile, env map[string]string) Quota
}

// Task is one pending +burn item exported from the task companion.
type Task struct {
	UUID        string   `json:"uuid"`
	ID          int      `json:"id,omitempty"`
	Description string   `json:"description"`
	Project     string   `json:"project,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Urgency     float64  `json:"urgency,omitempty"`
	Effort      string   `json:"effort,omitempty"` // light, standard, pro
	Dir         string   `json:"dir,omitempty"`    // optional UDA: repo path
}

// ShortID returns the first 8 characters of UUID (or the whole UUID if shorter).
func (t Task) ShortID() string {
	if len(t.UUID) <= 8 {
		return t.UUID
	}
	return t.UUID[:8]
}

// AgentName returns the deterministic burn agent name for this task.
func (t Task) AgentName() string { return "burn-" + t.ShortID() }

// NormalizedEffort returns light, standard or pro (default standard).
func (t Task) NormalizedEffort() string {
	switch t.Effort {
	case "light", "standard", "pro":
		return t.Effort
	default:
		return "standard"
	}
}

// DecisionKind classifies what the planner decided for a profile or agent.
type DecisionKind string

const (
	DecisionSpawn  DecisionKind = "spawn"
	DecisionWrapUp DecisionKind = "wrap_up"
	DecisionSkip   DecisionKind = "skip"
)

// ProfilePlan is the planner's evaluation for one burnable profile.
type ProfilePlan struct {
	Profile       string        `json:"profile"`
	Reader        string        `json:"reader"`
	Quota         Quota         `json:"quota"`
	Window        *Window       `json:"window,omitempty"`
	TimeToReset   time.Duration `json:"time_to_reset_ns,omitempty"`
	InWindow      bool          `json:"in_window"`
	MeanCost      float64       `json:"mean_cost,omitempty"`
	CostUnit      string        `json:"cost_unit,omitempty"` // "pct" or "tokens"
	Samples       int           `json:"samples,omitempty"`
	ComputedN     int           `json:"computed_n"`
	CappedN       int           `json:"capped_n"`
	SkipReason    string        `json:"skip_reason,omitempty"`
	PlannedSpawns []SpawnPlan   `json:"planned_spawns,omitempty"`
	WrapUps       []WrapUpPlan  `json:"wrap_ups,omitempty"`
}

// SpawnPlan is one agent that would be (or was) spawned in this tick.
type SpawnPlan struct {
	Agent   string `json:"agent"`
	Profile string `json:"profile"`
	Task    Task   `json:"task"`
	Dir     string `json:"dir"`
	Effort  string `json:"effort"`
	Prompt  string `json:"prompt"`
	Spawned bool   `json:"spawned,omitempty"`
	Error   string `json:"error,omitempty"`
}

// WrapUpPlan is one running burn agent asked to finish before reset.
type WrapUpPlan struct {
	Agent       string        `json:"agent"`
	Profile     string        `json:"profile"`
	TimeToReset time.Duration `json:"time_to_reset_ns"`
	Sent        bool          `json:"sent,omitempty"`
	Error       string        `json:"error,omitempty"`
}

// TickResult is the full outcome of one fleet burn / --tick evaluation.
type TickResult struct {
	At         time.Time     `json:"at"`
	DryRun     bool          `json:"dry_run"`
	TasksFound int           `json:"tasks_found"`
	TaskError  string        `json:"task_error,omitempty"`
	Profiles   []ProfilePlan `json:"profiles"`
}
