package prefs

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/conf"
)

// Burn holds the [burn] and [burn.<profile>] settings (ADR-190).
// Phase 1 defaults to DryRun = true so nothing spawns until opted in.
type Burn struct {
	// DryRun logs what fleet burn would spawn or wrap up without acting.
	// Default true.
	DryRun bool
	// Profiles lists the tool profiles allowed to burn leftover quota.
	// Empty means nothing is burned; exclusions live in config, not code.
	Profiles []string
	// Window is how far before a quota reset burning begins (default 18h).
	Window time.Duration
	// ReservePct is the remaining quota percentage to keep untouched (default 15).
	ReservePct float64
	// WrapUpBefore asks running burn agents to commit and finish this long
	// before the quota resets (default 30m).
	WrapUpBefore time.Duration
	// MaxPerTick caps new spawns per profile per tick (default 2).
	MaxPerTick int
	// MaxConcurrent caps total running burn agents across all profiles (default 3).
	MaxConcurrent int
	// DefaultCostPct is the fallback % of quota one run consumes when runs.jsonl
	// has fewer than MinSamples runs for that profile (default 5).
	DefaultCostPct float64
	// DefaultCostTokens is the fallback token cost per run for token-denominated
	// quotas (default 2_000_000).
	DefaultCostTokens int64
	// MinSamples is the minimum runs.jsonl entries before using empirical mean (default 3).
	MinSamples int
	// Tag is the task tag marking burnable work (default "burn").
	Tag string
	// AllowOverage permits burning even when a reader reports paid overage/extra
	// usage enabled (default false).
	AllowOverage bool
	// PerProfile holds [burn.<profile>] overrides.
	PerProfile map[string]BurnProfile
}

// BurnProfile overrides [burn] defaults for one profile.
type BurnProfile struct {
	// Reader selects the quota reader: "agy", "nanogpt", or "ai-usagebar".
	// Empty infers from the profile name or command.
	Reader string
	// Account selects a specific account/vendor entry in ai-usagebar.
	Account string
	// QuotaWindow picks which window to track (e.g. "weekly", "5h"; default "weekly").
	QuotaWindow string
	// Window overrides [burn] window for this profile when > 0.
	Window time.Duration
	// ReservePct overrides [burn] reserve_pct when >= 0 (-1 = unset).
	ReservePct float64
	// MaxPerTick overrides [burn] max_per_tick when > 0.
	MaxPerTick int
	// DefaultCostPct overrides [burn] default_cost_pct when > 0.
	DefaultCostPct float64
	// DefaultCostTokens overrides [burn] default_cost_tokens when > 0.
	DefaultCostTokens int64
	// LimitTokens optionally maps a percentage quota to a weekly token budget
	// so runs.jsonl token spend can estimate percentage cost.
	LimitTokens int64
	// TokenEnv names the environment variable holding an API/management token
	// (for nanogpt). Never holds a secret itself.
	TokenEnv string
	// UsageURL overrides the usage API URL (for nanogpt).
	UsageURL string
	// AllowOverage overrides [burn] allow_overage when set.
	AllowOverage *bool
	// Projects optionally restricts this profile to tasks whose project
	// matches one of these names (case-insensitive). Empty = any project.
	Projects []string
}

// Defaults for [burn] (ADR-190).
const (
	DefaultBurnWindow        = 18 * time.Hour
	DefaultBurnReservePct    = 15.0
	DefaultBurnWrapUpBefore  = 30 * time.Minute
	DefaultBurnMaxPerTick    = 2
	DefaultBurnMaxConcurrent = 3
	DefaultBurnCostPct       = 5.0
	DefaultBurnCostTokens    = int64(2_000_000)
	DefaultBurnMinSamples    = 3
	DefaultBurnTag           = "burn"
)

// DefaultBurn returns the safe Phase 1 defaults: dry-run on, no profiles enabled.
func DefaultBurn() Burn {
	return Burn{
		DryRun:            true,
		Window:            DefaultBurnWindow,
		ReservePct:        DefaultBurnReservePct,
		WrapUpBefore:      DefaultBurnWrapUpBefore,
		MaxPerTick:        DefaultBurnMaxPerTick,
		MaxConcurrent:     DefaultBurnMaxConcurrent,
		DefaultCostPct:    DefaultBurnCostPct,
		DefaultCostTokens: DefaultBurnCostTokens,
		MinSamples:        DefaultBurnMinSamples,
		Tag:               DefaultBurnTag,
		PerProfile:        map[string]BurnProfile{},
	}
}

// Burn returns [burn] and [burn.<profile>] settings.
func (p *Prefs) Burn() Burn {
	if p == nil {
		return DefaultBurn()
	}
	return p.burn
}

// ForProfile resolves the effective settings for profile by merging [burn.<profile>]
// over the [burn] defaults.
func (b Burn) ForProfile(profile string) BurnProfile {
	eff := BurnProfile{
		QuotaWindow:       "weekly",
		Window:            b.Window,
		ReservePct:        b.ReservePct,
		MaxPerTick:        b.MaxPerTick,
		DefaultCostPct:    b.DefaultCostPct,
		DefaultCostTokens: b.DefaultCostTokens,
	}
	ov, ok := b.PerProfile[profile]
	if !ok {
		return eff
	}
	eff.Reader = ov.Reader
	eff.Account = ov.Account
	eff.TokenEnv = ov.TokenEnv
	eff.UsageURL = ov.UsageURL
	eff.LimitTokens = ov.LimitTokens
	eff.AllowOverage = ov.AllowOverage
	if len(ov.Projects) > 0 {
		eff.Projects = append([]string(nil), ov.Projects...)
	}
	if ov.QuotaWindow != "" {
		eff.QuotaWindow = ov.QuotaWindow
	}
	if ov.Window > 0 {
		eff.Window = ov.Window
	}
	if ov.ReservePct >= 0 {
		eff.ReservePct = ov.ReservePct
	}
	if ov.MaxPerTick > 0 {
		eff.MaxPerTick = ov.MaxPerTick
	}
	if ov.DefaultCostPct > 0 {
		eff.DefaultCostPct = ov.DefaultCostPct
	}
	if ov.DefaultCostTokens > 0 {
		eff.DefaultCostTokens = ov.DefaultCostTokens
	}
	return eff
}

// OverageAllowed reports whether overage is allowed for profile.
func (b Burn) OverageAllowed(profile string) bool {
	if ov, ok := b.PerProfile[profile]; ok && ov.AllowOverage != nil {
		return *ov.AllowOverage
	}
	return b.AllowOverage
}

func applyBurn(b Burn, sec map[string]conf.Value, path string) (Burn, error) {
	if v, ok := conf.Bool(sec, "dry_run"); ok {
		b.DryRun = v
	}
	if v, ok := sec["profiles"]; ok {
		if ps, ok := v.([]string); ok {
			b.Profiles = append([]string(nil), ps...)
		}
	}
	if v, ok := conf.Dur(sec, "window"); ok {
		if v <= 0 {
			return b, fmt.Errorf("%s: [burn] window: want a duration > 0", path)
		}
		b.Window = v
	}
	if v := strings.TrimSpace(conf.Str(sec, "reserve_pct")); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || f >= 100 {
			return b, fmt.Errorf("%s: [burn] reserve_pct %q: want a number in [0, 100)", path, v)
		}
		b.ReservePct = f
	}
	if v, ok := conf.Dur(sec, "wrap_up_before"); ok {
		if v < 0 {
			return b, fmt.Errorf("%s: [burn] wrap_up_before: want a duration >= 0", path)
		}
		b.WrapUpBefore = v
	}
	if v := strings.TrimSpace(conf.Str(sec, "max_per_tick")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return b, fmt.Errorf("%s: [burn] max_per_tick %q: want a whole number >= 1", path, v)
		}
		b.MaxPerTick = n
	}
	if v := strings.TrimSpace(conf.Str(sec, "max_concurrent")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return b, fmt.Errorf("%s: [burn] max_concurrent %q: want a whole number >= 1", path, v)
		}
		b.MaxConcurrent = n
	}
	if v := strings.TrimSpace(conf.Str(sec, "default_cost_pct")); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f <= 0 {
			return b, fmt.Errorf("%s: [burn] default_cost_pct %q: want a number > 0", path, v)
		}
		b.DefaultCostPct = f
	}
	if v := strings.TrimSpace(conf.Str(sec, "default_cost_tokens")); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return b, fmt.Errorf("%s: [burn] default_cost_tokens %q: want a whole number > 0", path, v)
		}
		b.DefaultCostTokens = n
	}
	if v := strings.TrimSpace(conf.Str(sec, "min_samples")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return b, fmt.Errorf("%s: [burn] min_samples %q: want a whole number >= 1", path, v)
		}
		b.MinSamples = n
	}
	if v := strings.TrimSpace(conf.Str(sec, "tag")); v != "" {
		b.Tag = strings.TrimPrefix(v, "+")
	}
	if v, ok := conf.Bool(sec, "allow_overage"); ok {
		b.AllowOverage = v
	}
	return b, nil
}

func applyBurnProfile(sec map[string]conf.Value, path, name string) (BurnProfile, error) {
	bp := BurnProfile{ReservePct: -1}
	bp.Reader = strings.TrimSpace(conf.Str(sec, "reader"))
	bp.Account = strings.TrimSpace(conf.Str(sec, "account"))
	bp.QuotaWindow = strings.TrimSpace(conf.Str(sec, "quota_window"))
	bp.TokenEnv = strings.TrimSpace(conf.Str(sec, "token_env"))
	bp.UsageURL = strings.TrimSpace(conf.Str(sec, "usage_url"))
	if v, ok := conf.Dur(sec, "window"); ok {
		if v <= 0 {
			return bp, fmt.Errorf("%s: [burn.%s] window: want a duration > 0", path, name)
		}
		bp.Window = v
	}
	if v := strings.TrimSpace(conf.Str(sec, "reserve_pct")); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || f >= 100 {
			return bp, fmt.Errorf("%s: [burn.%s] reserve_pct %q: want a number in [0, 100)", path, name, v)
		}
		bp.ReservePct = f
	}
	if v := strings.TrimSpace(conf.Str(sec, "max_per_tick")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return bp, fmt.Errorf("%s: [burn.%s] max_per_tick %q: want a whole number >= 1", path, name, v)
		}
		bp.MaxPerTick = n
	}
	if v := strings.TrimSpace(conf.Str(sec, "default_cost_pct")); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f <= 0 {
			return bp, fmt.Errorf("%s: [burn.%s] default_cost_pct %q: want a number > 0", path, name, v)
		}
		bp.DefaultCostPct = f
	}
	if v := strings.TrimSpace(conf.Str(sec, "default_cost_tokens")); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return bp, fmt.Errorf("%s: [burn.%s] default_cost_tokens %q: want a whole number > 0", path, name, v)
		}
		bp.DefaultCostTokens = n
	}
	if v := strings.TrimSpace(conf.Str(sec, "limit_tokens")); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return bp, fmt.Errorf("%s: [burn.%s] limit_tokens %q: want a whole number > 0", path, name, v)
		}
		bp.LimitTokens = n
	}
	if v, ok := conf.Bool(sec, "allow_overage"); ok {
		bp.AllowOverage = &v
	}
	if v, ok := sec["projects"]; ok {
		if ps, ok := v.([]string); ok {
			bp.Projects = append([]string(nil), ps...)
		}
	}
	return bp, nil
}
