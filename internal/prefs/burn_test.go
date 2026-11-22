package prefs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBurnDefaultsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	t.Setenv("FLEET_CONFIG", cfg)

	// Missing config -> safe Phase 1 defaults.
	p, err := Load()
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	b := p.Burn()
	if !b.DryRun {
		t.Fatal("default DryRun = false, want true")
	}
	if len(b.Profiles) != 0 {
		t.Fatalf("default Profiles = %v, want empty", b.Profiles)
	}
	if b.Window != DefaultBurnWindow || b.ReservePct != DefaultBurnReservePct || b.WrapUpBefore != DefaultBurnWrapUpBefore {
		t.Fatalf("unexpected defaults: %+v", b)
	}
	if b.Tag != "burn" {
		t.Fatalf("default Tag = %q, want burn", b.Tag)
	}

	// Valid config with [burn] and [burn.<profile>] overrides.
	toml := `
[burn]
dry_run = false
profiles = ["agy", "pi", "claude-team-a"]
window = "12h"
reserve_pct = 20
wrap_up_before = "45m"
max_per_tick = 3
max_concurrent = 4
default_cost_pct = 8.5
default_cost_tokens = 3000000
min_samples = 5
tag = "+burn"
allow_overage = false

[burn.pi]
reader = "nanogpt"
window = "24h"
reserve_pct = 10
token_env = "NANOGPT_MANAGEMENT_TOKEN"

[burn.claude-team-a]
reader = "ai-usagebar"
account = "team-a"
quota_window = "5h"
max_per_tick = 1
allow_overage = true
`
	if err := os.WriteFile(cfg, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err = Load()
	if err != nil {
		t.Fatalf("Load valid: %v", err)
	}
	b = p.Burn()
	if b.DryRun {
		t.Fatal("DryRun = true, want false")
	}
	if len(b.Profiles) != 3 || b.Profiles[0] != "agy" || b.Profiles[2] != "claude-team-a" {
		t.Fatalf("Profiles = %v", b.Profiles)
	}
	if b.Tag != "burn" {
		t.Fatalf("Tag = %q, want burn (+ stripped)", b.Tag)
	}
	if b.Window != 12*time.Hour || b.ReservePct != 20 || b.WrapUpBefore != 45*time.Minute {
		t.Fatalf("unexpected [burn] values: %+v", b)
	}

	pi := b.ForProfile("pi")
	if pi.Reader != "nanogpt" || pi.Window != 24*time.Hour || pi.ReservePct != 10 || pi.TokenEnv != "NANOGPT_MANAGEMENT_TOKEN" {
		t.Fatalf("unexpected pi profile: %+v", pi)
	}
	if pi.MaxPerTick != 3 {
		t.Fatalf("pi MaxPerTick = %d, want inherited 3", pi.MaxPerTick)
	}

	ct := b.ForProfile("claude-team-a")
	if ct.Reader != "ai-usagebar" || ct.Account != "team-a" || ct.QuotaWindow != "5h" || ct.MaxPerTick != 1 {
		t.Fatalf("unexpected claude-team-a profile: %+v", ct)
	}
	if !b.OverageAllowed("claude-team-a") || b.OverageAllowed("pi") {
		t.Fatalf("unexpected OverageAllowed: team-a=%v pi=%v", b.OverageAllowed("claude-team-a"), b.OverageAllowed("pi"))
	}
}

func TestBurnValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		toml string
	}{
		{"bad_window", "[burn]\nwindow = \"0s\"\n"},
		{"bad_reserve", "[burn]\nreserve_pct = 100\n"},
		{"bad_max_per_tick", "[burn]\nmax_per_tick = 0\n"},
		{"bad_profile_reserve", "[burn.pi]\nreserve_pct = -5\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := filepath.Join(t.TempDir(), "config.toml")
			t.Setenv("FLEET_CONFIG", cfg)
			if err := os.WriteFile(cfg, []byte(tc.toml), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(); err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}
