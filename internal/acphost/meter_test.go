package acphost

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/sturlabragason/fleet/internal/acp"
	"github.com/sturlabragason/fleet/internal/spend"
)

func TestFooter(t *testing.T) {
	now := time.Now()
	rl := func(typ string, util float64, in time.Duration) acp.SessionUpdate {
		meta, _ := json.Marshal(map[string]any{"_claude/model": "claude-opus-5-5",
			"_claude/rateLimit": map[string]any{"rateLimitType": typ, "utilization": util, "resetsAt": now.Add(in).Unix()}})
		return acp.SessionUpdate{Kind: acp.UpdateUsage, Used: 50, Size: 200, Cost: &acp.Cost{Amount: 1.2, Currency: "USD"}, Meta: meta}
	}
	tests := []struct {
		name string
		set  func(m *meter)
		w    int
		want string
	}{
		{"nothing known", func(*meter) {}, 120, ""},
		{"claude windows", func(m *meter) {
			m.usage(rl("seven_day", 0.2, 72*time.Hour+time.Minute))
			m.usage(rl("five_hour", 0.4, 4*time.Hour+56*time.Minute+30*time.Second))
		}, 120, "5h ▰▰▱▱ 60% left (4h56m)  7d ▰▱▱▱ 80% left (3d)  ctx ▰▱▱▱ 25%  claude-opus-5-5  $1.20"},
		{"stale window left out", func(m *meter) { m.usage(rl("five_hour", 0.9, -time.Minute)) }, 120, "ctx ▰▱▱▱ 25%  claude-opus-5-5  $1.20"},
		{"narrow drops from the end", func(m *meter) { m.usage(rl("five_hour", 0.4, time.Hour+30*time.Second)) }, 36, "5h ▰▰▱▱ 60% left (1h00m)"},
		{"copilot", func(m *meter) {
			m.setModels(acp.Models{Current: "claude-opus-5.5", Available: []acp.ModelInfo{{ModelID: "claude-opus-5.5", Name: "Claude Opus 5.5"}}})
			m.spend(spend.Spend{Calls: 1, AIC: 12.3, HasAIC: true, Context: 34, Model: "claude-opus-5.5",
				Quota: &spend.Quota{LeftPct: 62, Resets: now.Add(14*24*time.Hour + time.Minute)}})
		}, 120, "premium ▰▰▱▱ 62% left (14d)  ctx ▰▱▱▱ 34%  Claude Opus 5.5  12.3 AIC"},
		{"copilot unlimited has no window", func(m *meter) {
			m.spend(spend.Spend{Calls: 1, AIC: 1, HasAIC: true, Quota: &spend.Quota{Unlimited: true, LeftPct: 100}})
		}, 120, "1.0 AIC"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := newView(io.Discard, nil, colorprofile.Ascii, "a1", "")
			tc.set(&v.meter)
			got := strings.TrimSpace(ansi.Strip(v.footer(tc.w)))
			if got != tc.want {
				t.Fatalf("footer\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}
