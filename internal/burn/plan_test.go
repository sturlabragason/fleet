package burn

import (
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/runs"
)

func TestPlanSizingAndGates(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	cfg := prefs.DefaultBurn()
	cfg.Profiles = []string{"pi", "agy", "claude-team"}
	cfg.Window = 18 * time.Hour
	cfg.ReservePct = 15
	cfg.WrapUpBefore = 30 * time.Minute
	cfg.MaxPerTick = 2
	cfg.MaxConcurrent = 3

	// 4 runs for pi averaging 5,000,000 tokens each -> above min_samples (3).
	history := []runs.Record{
		{Profile: "pi", Spend: &runs.Spend{Tokens: 4_000_000}},
		{Profile: "pi", Spend: &runs.Spend{Tokens: 6_000_000}},
		{Profile: "pi", Spend: &runs.Spend{Tokens: 5_000_000}},
	}

	quotas := []Quota{
		{
			// pi: 48M remaining of 60M (80%), reset in 6h (in window).
			// reserve = 9M, avail = 39M, mean = 5M -> N = 7, capped at MaxPerTick = 2.
			Profile: "pi", Reader: "nanogpt", Available: true,
			Windows: []Window{{
				Kind: "weekly", RemainingPct: 80, LimitTokens: 60_000_000, RemainingTokens: 48_000_000,
				ResetAt: now.Add(6 * time.Hour),
			}},
		},
		{
			// agy: 95% remaining, reset in 5 days -> outside 18h window.
			Profile: "agy", Reader: "agy", Available: true,
			Windows: []Window{{
				Kind: "weekly", RemainingPct: 95, ResetAt: now.Add(5 * 24 * time.Hour),
			}},
		},
		{
			// claude-team: overage ON -> skipped by safety gate.
			Profile: "claude-team", Reader: "ai-usagebar", Available: true, OverageOn: true,
			Windows: []Window{{
				Kind: "weekly", RemainingPct: 70, ResetAt: now.Add(4 * time.Hour),
			}},
		},
	}

	tasks := []Task{
		{UUID: "11111111-aaaa-bbbb-cccc-000000000001", Description: "first task", Effort: "pro", Urgency: 10},
		{UUID: "22222222-aaaa-bbbb-cccc-000000000002", Description: "second task", Effort: "light", Urgency: 8},
		{UUID: "33333333-aaaa-bbbb-cccc-000000000003", Description: "already running", Urgency: 12},
	}

	active := []ActiveAgent{
		{Name: "burn-33333333", Profile: "agy", Working: true},
	}

	res := Plan(PlanInput{
		Now:        now,
		Config:     cfg,
		MaxWorking: 8,
		Quotas:     quotas,
		Runs:       history,
		Tasks:      tasks,
		Active:     active,
		DefaultDir: "/tmp/repo",
	})

	if !res.DryRun {
		t.Fatal("expected DryRun true")
	}
	if len(res.Profiles) != 3 {
		t.Fatalf("got %d profiles, want 3", len(res.Profiles))
	}

	// pi
	pi := res.Profiles[0]
	if !pi.InWindow || pi.ComputedN != 7 || pi.CappedN != 2 || pi.Samples != 3 {
		t.Fatalf("pi plan mismatch: %+v", pi)
	}
	if len(pi.PlannedSpawns) != 2 {
		t.Fatalf("pi PlannedSpawns = %d, want 2", len(pi.PlannedSpawns))
	}
	if pi.PlannedSpawns[0].Agent != "burn-11111111" || pi.PlannedSpawns[0].Effort != "pro" {
		t.Fatalf("first spawn = %+v", pi.PlannedSpawns[0])
	}
	if pi.PlannedSpawns[1].Agent != "burn-22222222" || pi.PlannedSpawns[1].Effort != "light" {
		t.Fatalf("second spawn = %+v", pi.PlannedSpawns[1])
	}

	// agy outside window
	agy := res.Profiles[1]
	if agy.InWindow || !strings.Contains(agy.SkipReason, "outside burn window") {
		t.Fatalf("agy plan = %+v", agy)
	}

	// claude-team overage gate
	ct := res.Profiles[2]
	if !strings.Contains(ct.SkipReason, "overage") {
		t.Fatalf("claude-team skip = %q, want overage reason", ct.SkipReason)
	}
}

func TestPlanWrapUpAndReserve(t *testing.T) {
	now := time.Date(2026, 11, 22, 23, 40, 0, 0, time.UTC)
	cfg := prefs.DefaultBurn()
	cfg.WrapUpBefore = 30 * time.Minute
	cfg.ReservePct = 15

	res := Plan(PlanInput{
		Now:    now,
		Config: cfg,
		Quotas: []Quota{
			{
				Profile: "pi", Reader: "nanogpt", Available: true,
				Windows: []Window{{Kind: "weekly", RemainingPct: 50, ResetAt: now.Add(20 * time.Minute)}},
			},
			{
				Profile: "agy", Reader: "agy", Available: true,
				Windows: []Window{{Kind: "weekly", RemainingPct: 12, ResetAt: now.Add(2 * time.Hour)}},
			},
		},
		Active: []ActiveAgent{
			{Name: "burn-aaaaaaaa", Profile: "pi", Working: true},
			{Name: "regular-agent", Profile: "pi", Working: true},
		},
	})

	if len(res.Profiles[0].WrapUps) != 1 || res.Profiles[0].WrapUps[0].Agent != "burn-aaaaaaaa" {
		t.Fatalf("expected 1 wrap-up for burn-aaaaaaaa, got %+v", res.Profiles[0].WrapUps)
	}
	if !strings.Contains(res.Profiles[1].SkipReason, "reserve") {
		t.Fatalf("expected reserve skip, got %q", res.Profiles[1].SkipReason)
	}
}

func TestPlanProjectAllowlist(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	cfg := prefs.DefaultBurn()
	cfg.PerProfile["claude-scoped"] = prefs.BurnProfile{
		ReservePct: -1,
		Projects:   []string{"client-a"},
	}
	res := Plan(PlanInput{
		Now:    now,
		Config: cfg,
		Quotas: []Quota{{
			Profile: "claude-scoped", Reader: "ai-usagebar", Available: true,
			Windows: []Window{{Kind: "weekly", RemainingPct: 80, ResetAt: now.Add(2 * time.Hour)}},
		}},
		Tasks: []Task{
			{UUID: "aaaaaaaa-0000-0000-0000-000000000001", Description: "other project", Project: "internal", Urgency: 10},
			{UUID: "bbbbbbbb-0000-0000-0000-000000000002", Description: "allowed project", Project: "client-a", Urgency: 5},
		},
	})
	spawns := res.Profiles[0].PlannedSpawns
	if len(spawns) != 1 || spawns[0].Agent != "burn-bbbbbbbb" {
		t.Fatalf("expected only client-a task burn-bbbbbbbb, got %+v", spawns)
	}
}

func TestRenderReviewHTML(t *testing.T) {
	html, err := RenderReviewHTML(time.Date(2026, 11, 23, 7, 0, 0, 0, time.UTC), []BranchSummary{
		{Branch: "fleet/burn-11111111", Commits: 2, Subject: "abc1234 feat: add parser tests", Stat: " 1 file changed, 20 insertions(+)"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fleet/burn-11111111", "Accept", "Reject", "Copy Decisions JSON"} {
		if !strings.Contains(html, want) {
			t.Errorf("review html missing %q", want)
		}
	}
}
