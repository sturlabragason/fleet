package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/burn"
)

func TestWriteBurnText(t *testing.T) {
	now := time.Date(2026, 11, 22, 21, 0, 0, 0, time.UTC)
	res := burn.TickResult{
		At:         now,
		DryRun:     true,
		TasksFound: 1,
		Profiles: []burn.ProfilePlan{
			{
				Profile:     "pi",
				Reader:      "nanogpt",
				Window:      &burn.Window{Kind: "weekly", RemainingPct: 80, RemainingTokens: 48_000_000, LimitTokens: 60_000_000, ResetAt: now.Add(4 * time.Hour)},
				TimeToReset: 4 * time.Hour,
				InWindow:    true,
				ComputedN:   10,
				CappedN:     1,
				PlannedSpawns: []burn.SpawnPlan{
					{
						Agent:   "burn-11111111",
						Profile: "pi",
						Effort:  "standard",
						Task:    burn.Task{UUID: "11111111-aaaa-bbbb-cccc-000000000001", Description: "add parser tests"},
					},
				},
			},
			{
				Profile:    "agy",
				Reader:     "agy",
				SkipReason: "outside burn window",
			},
		},
	}

	var buf bytes.Buffer
	writeBurnText(&buf, res)
	out := buf.String()
	for _, want := range []string{"fleet burn [DRY-RUN]", "pi", "80.0% (48M/60M)", "would spawn 1 agent(s)", "would spawn burn-11111111", "outside burn window"} {
		if !strings.Contains(out, want) {
			t.Errorf("writeBurnText missing %q in:\n%s", want, out)
		}
	}
}
