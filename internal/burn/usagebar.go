package burn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
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
	Hint:    "install ai-usagebar from https://github.com/akitaonrails/ai-usagebar",
})

// UsageBarReader reads quota snapshots from `ai-usagebar --json`.
type UsageBarReader struct{}

// Name returns the reader identifier.
func (UsageBarReader) Name() string { return "ai-usagebar" }

// Read runs `ai-usagebar --json` via companion and parses the matching entry.
func (r UsageBarReader) Read(ctx context.Context, profile string, cfg prefs.BurnProfile, env map[string]string) Quota {
	now := time.Now().UTC()
	q := Quota{
		Profile:   profile,
		Reader:    r.Name(),
		FetchedAt: now,
	}

	tool, err := companion.Find(UsageBarSpec)
	if err != nil {
		q.Available = false
		q.Error = err.Error()
		if errors.Is(err, companion.ErrMissing) || errors.Is(err, companion.ErrOff) {
			q.Hint = UsageBarSpec.Hint
		}
		return q
	}

	out, err := tool.Run(ctx, companion.Opts{
		Timeout: 15 * time.Second,
		Env:     mapToEnvSlice(env),
	}, "--json")
	if err != nil {
		q.Available = false
		q.Error = err.Error()
		return q
	}

	windows, overageOn, err := parseUsageBarForProfile(out, cfg.Account, profile)
	if err != nil {
		q.Available = false
		q.Error = err.Error()
		return q
	}

	q.Available = true
	q.OverageOn = overageOn
	q.Windows = windows
	return q
}

func parseUsageBarForProfile(out []byte, account, profile string) ([]Window, bool, error) {
	if account != "" {
		return ParseUsageBarJSON(out, account)
	}
	if profile != "" {
		if windows, overage, err := ParseUsageBarJSON(out, profile); err == nil {
			return windows, overage, nil
		}
		if strings.HasPrefix(strings.ToLower(profile), "claude") {
			if windows, overage, err := ParseUsageBarJSON(out, "anthropic"); err == nil {
				return windows, overage, nil
			}
			if windows, overage, err := ParseUsageBarJSON(out, "claude"); err == nil {
				return windows, overage, nil
			}
		}
	}
	return ParseUsageBarJSON(out, "")
}

type usageBarEntry struct {
	Account    string           `json:"account"`
	Name       string           `json:"name"`
	Vendor     string           `json:"vendor"`
	ID         string           `json:"id"`
	Provider   string           `json:"provider"`
	ExtraUsage bool             `json:"extra_usage"`
	Overage    bool             `json:"overage"`
	OverageOn  bool             `json:"overage_on"`
	Windows    []usageBarWindow `json:"windows"`
	Limits     []usageBarWindow `json:"limits"`
	Quotas     []usageBarWindow `json:"quotas"`
}

func (e usageBarEntry) groupName() string {
	for _, s := range []string{e.Account, e.Name, e.Vendor, e.Provider, e.ID} {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return "ai-usagebar"
}

func (e usageBarEntry) identifiers() []string {
	var out []string
	for _, s := range []string{e.Account, e.Name, e.Vendor, e.ID, e.Provider} {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (e usageBarEntry) rawWindows() []usageBarWindow {
	if len(e.Windows) > 0 {
		return e.Windows
	}
	if len(e.Limits) > 0 {
		return e.Limits
	}
	return e.Quotas
}

type usageBarWindow struct {
	Name         string          `json:"name"`
	Label        string          `json:"label"`
	Kind         string          `json:"kind"`
	Type         string          `json:"type"`
	RemainingPct *float64        `json:"remaining_pct"`
	Remaining    *float64        `json:"remaining"`
	UsedPct      *float64        `json:"used_pct"`
	PercentUsed  *float64        `json:"percent_used"`
	Percent      *float64        `json:"percent"`
	ResetsAt     json.RawMessage `json:"resets_at"`
	ResetAt      json.RawMessage `json:"reset_at"`
}

type usageBarWrapper struct {
	Vendors   []usageBarEntry  `json:"vendors"`
	Accounts  []usageBarEntry  `json:"accounts"`
	Providers []usageBarEntry  `json:"providers"`
	Windows   []usageBarWindow `json:"windows"`
	Limits    []usageBarWindow `json:"limits"`
	Quotas    []usageBarWindow `json:"quotas"`
	Account   string           `json:"account"`
	Name      string           `json:"name"`
	Vendor    string           `json:"vendor"`
	ID        string           `json:"id"`
	Extra     bool             `json:"extra_usage"`
	Overage   bool             `json:"overage"`
	OverageOn bool             `json:"overage_on"`
}

// ParseUsageBarJSON parses the JSON output of `ai-usagebar --json`.
// It supports top-level arrays, wrapper objects ({vendors/accounts/providers}),
// and single-entry objects with windows.
func ParseUsageBarJSON(data []byte, account string) ([]Window, bool, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, false, errors.New("empty ai-usagebar output")
	}

	entries, err := decodeUsageBarEntries(trimmed)
	if err != nil {
		return nil, false, err
	}
	if len(entries) == 0 {
		return nil, false, errors.New("no provider entries in ai-usagebar output")
	}

	selected, err := filterUsageBarEntries(entries, account)
	if err != nil {
		return nil, false, err
	}

	var windows []Window
	overageOn := false
	for _, entry := range selected {
		if entry.ExtraUsage || entry.Overage || entry.OverageOn {
			overageOn = true
		}
		group := entry.groupName()
		for _, raw := range entry.rawWindows() {
			w, err := convertUsageBarWindow(group, raw)
			if err != nil {
				return nil, false, err
			}
			windows = append(windows, w)
		}
	}
	if len(windows) == 0 {
		return nil, overageOn, errors.New("no quota windows in ai-usagebar output")
	}
	return windows, overageOn, nil
}

func decodeUsageBarEntries(trimmed []byte) ([]usageBarEntry, error) {
	if trimmed[0] == '[' {
		var entries []usageBarEntry
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			return nil, fmt.Errorf("parse ai-usagebar array: %w", err)
		}
		return entries, nil
	}

	var wrap usageBarWrapper
	if err := json.Unmarshal(trimmed, &wrap); err != nil {
		return nil, fmt.Errorf("parse ai-usagebar object: %w", err)
	}
	switch {
	case len(wrap.Vendors) > 0:
		return wrap.Vendors, nil
	case len(wrap.Accounts) > 0:
		return wrap.Accounts, nil
	case len(wrap.Providers) > 0:
		return wrap.Providers, nil
	case len(wrap.Windows) > 0 || len(wrap.Limits) > 0 || len(wrap.Quotas) > 0:
		return []usageBarEntry{{
			Account:    wrap.Account,
			Name:       wrap.Name,
			Vendor:     wrap.Vendor,
			ID:         wrap.ID,
			ExtraUsage: wrap.Extra,
			Overage:    wrap.Overage,
			OverageOn:  wrap.OverageOn,
			Windows:    wrap.Windows,
			Limits:     wrap.Limits,
			Quotas:     wrap.Quotas,
		}}, nil
	default:
		return nil, nil
	}
}

func filterUsageBarEntries(entries []usageBarEntry, account string) ([]usageBarEntry, error) {
	if account == "" {
		if len(entries) == 1 {
			return entries, nil
		}
		return nil, fmt.Errorf("ai-usagebar returned %d entries; set [burn.<profile>] account to pick one", len(entries))
	}
	needle := strings.ToLower(strings.TrimSpace(account))

	var exact []usageBarEntry
	var sub []usageBarEntry
	for _, entry := range entries {
		matchedExact := false
		matchedSub := false
		for _, id := range entry.identifiers() {
			lower := strings.ToLower(id)
			if lower == needle {
				matchedExact = true
				break
			}
			if strings.Contains(lower, needle) {
				matchedSub = true
			}
		}
		if matchedExact {
			exact = append(exact, entry)
		} else if matchedSub {
			sub = append(sub, entry)
		}
	}
	if len(exact) > 0 {
		return exact, nil
	}
	if len(sub) > 0 {
		return sub, nil
	}
	return nil, fmt.Errorf("account %q not found in ai-usagebar output", account)
}

func convertUsageBarWindow(group string, raw usageBarWindow) (Window, error) {
	label := firstNonEmpty(raw.Label, raw.Name, raw.Kind, raw.Type)
	kindHint := firstNonEmpty(raw.Kind, raw.Type, raw.Name, raw.Label)
	kind := classifyWindowKind(kindHint)

	var remainingPct float64
	switch {
	case raw.RemainingPct != nil:
		remainingPct = *raw.RemainingPct
	case raw.Remaining != nil:
		remainingPct = *raw.Remaining
	case raw.UsedPct != nil:
		remainingPct = 100 - *raw.UsedPct
	case raw.PercentUsed != nil:
		remainingPct = 100 - *raw.PercentUsed
	case raw.Percent != nil:
		remainingPct = 100 - *raw.Percent
	default:
		return Window{}, fmt.Errorf("window %q missing remaining/used percentage", label)
	}
	remainingPct = math.Max(0, math.Min(100, remainingPct))

	resetRaw := raw.ResetsAt
	if len(bytes.TrimSpace(resetRaw)) == 0 || bytes.Equal(bytes.TrimSpace(resetRaw), []byte("null")) {
		resetRaw = raw.ResetAt
	}
	resetAt, err := parseUsageBarReset(resetRaw)
	if err != nil {
		return Window{}, fmt.Errorf("parse reset time for window %q: %w", label, err)
	}

	return Window{
		Group:        group,
		Kind:         kind,
		Label:        label,
		RemainingPct: remainingPct,
		ResetAt:      resetAt,
	}, nil
}

func parseUsageBarReset(raw json.RawMessage) (time.Time, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return time.Time{}, nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return time.Time{}, err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return time.Time{}, nil
		}
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t.UTC(), nil
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.UTC(), nil
		}
		return time.Time{}, fmt.Errorf("unrecognized reset timestamp %q", s)
	}

	num, err := strconv.ParseInt(string(trimmed), 10, 64)
	if err != nil {
		f, ferr := strconv.ParseFloat(string(trimmed), 64)
		if ferr != nil {
			return time.Time{}, fmt.Errorf("unrecognized reset timestamp %s", string(trimmed))
		}
		num = int64(f)
	}
	if num <= 0 {
		return time.Time{}, nil
	}
	if num < 1_000_000_000_000 {
		return time.Unix(num, 0).UTC(), nil
	}
	return time.UnixMilli(num).UTC(), nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
