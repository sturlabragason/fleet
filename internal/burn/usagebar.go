package burn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
	"github.com/sturlabragason/fleet/internal/prefs"
)

// UsageBarSpec declares the ai-usagebar companion binary for Claude quotas.
var UsageBarSpec = companion.Register(companion.Spec{
	Name:    "ai-usagebar",
	Bins:    []string{"ai-usagebar"},
	Env:     []string{"FLEET_AI_USAGEBAR"},
	Feature: "read Claude subscription quotas for fleet burn",
	Hint:    "install ai-usagebar from https://github.com/akitaonrails/ai-usagebar and add one [[anthropic.accounts]] per seat",
})

// UsageBarReader reads quota snapshots from `ai-usagebar usage --json`.
type UsageBarReader struct{}

// Name returns the reader identifier.
func (UsageBarReader) Name() string { return "ai-usagebar" }

// Read runs `ai-usagebar usage --json` via companion and parses the entry for
// the profile's account.
func (r UsageBarReader) Read(ctx context.Context, profile string, cfg prefs.BurnProfile, env map[string]string) Quota {
	q := Quota{Profile: profile, Reader: r.Name(), FetchedAt: time.Now().UTC()}

	tool, err := companion.Find(UsageBarSpec)
	if err != nil {
		q.Error = err.Error()
		if errors.Is(err, companion.ErrMissing) || errors.Is(err, companion.ErrOff) {
			q.Hint = UsageBarSpec.Hint
		}
		return q
	}
	out, err := tool.Run(ctx, companion.Opts{Timeout: 30 * time.Second, Env: mapToEnvSlice(env)}, "usage", "--json")
	if err != nil {
		q.Error = err.Error()
		return q
	}
	windows, overageOn, err := ParseUsageBarJSON(out, cfg.Account)
	if err != nil {
		q.Error = err.Error()
		return q
	}
	q.Available = true
	q.OverageOn = overageOn
	q.Windows = windows
	return q
}

// usageBarReport is the documented `ai-usagebar usage --json` document
// (schema_version 1): one entry per vendor account, e.g. "anthropic@work".
type usageBarReport struct {
	Entries []usageBarEntry `json:"entries"`
}

type usageBarEntry struct {
	ID      string           `json:"id"`
	Error   any              `json:"error"`
	Metrics []usageBarMetric `json:"metrics"`
}

type usageBarMetric struct {
	Label     string    `json:"label"`
	Percent   *float64  `json:"percent"` // used, 0..100
	ResetAt   time.Time `json:"reset_at"`
	ModelPool bool      `json:"model_pool"`
}

// ParseUsageBarJSON parses `ai-usagebar usage --json` and returns the quota
// windows of one account. Only account picks an entry ("work" matches
// "anthropic@work"), so one seat is never judged on another's quota; with no
// account the output is used only when it has one entry. An "Extra usage"
// metric means paid overage is on for that account.
func ParseUsageBarJSON(data []byte, account string) ([]Window, bool, error) {
	var rep usageBarReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, false, fmt.Errorf("parse ai-usagebar usage --json: %w", err)
	}
	e, err := pickUsageBarEntry(rep.Entries, account)
	if err != nil {
		return nil, false, err
	}
	if e.Error != nil {
		return nil, false, fmt.Errorf("ai-usagebar %s: %v", e.ID, e.Error)
	}
	var windows []Window
	overageOn := false
	for _, m := range e.Metrics {
		if strings.Contains(strings.ToLower(m.Label), "extra usage") {
			overageOn = true
			continue
		}
		// A per-model pool is one model's own limit, not the seat's window.
		if m.Percent == nil || m.ModelPool {
			continue
		}
		windows = append(windows, Window{
			Group:        e.ID,
			Kind:         classifyWindowKind(m.Label),
			Label:        m.Label,
			RemainingPct: max(0, min(100, 100-*m.Percent)),
			ResetAt:      m.ResetAt.UTC(),
		})
	}
	if len(windows) == 0 {
		return nil, overageOn, fmt.Errorf("no quota windows for %s in ai-usagebar output", e.ID)
	}
	return windows, overageOn, nil
}

func pickUsageBarEntry(entries []usageBarEntry, account string) (usageBarEntry, error) {
	if account == "" {
		if len(entries) == 1 {
			return entries[0], nil
		}
		return usageBarEntry{}, fmt.Errorf("ai-usagebar returned %d entries; set [burn.<profile>] account to pick one", len(entries))
	}
	for _, e := range entries {
		_, label, _ := strings.Cut(e.ID, "@")
		if strings.EqualFold(label, account) || strings.EqualFold(e.ID, account) {
			return e, nil
		}
	}
	return usageBarEntry{}, fmt.Errorf("account %q not found in ai-usagebar output (add it as [[anthropic.accounts]] label)", account)
}
