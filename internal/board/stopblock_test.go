package board

import (
	"testing"
	"time"
)

func TestBlockStop(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	prompt := now.Add(-10 * time.Minute)
	rep := func(state string, at time.Time) *Report { return &Report{State: state, At: at} }
	cases := []struct {
		name      string
		a         Agent
		active    bool
		blockedAt time.Time
		want      bool
	}{
		{"task, no report", Agent{PromptAt: prompt}, false, time.Time{}, true},
		{"card only, no report", Agent{Card: &Card{Goal: "g"}}, false, time.Time{}, true},
		{"no task", Agent{}, false, time.Time{}, false},
		{"done since prompt", Agent{PromptAt: prompt, Report: rep("done", now.Add(-time.Minute))}, false, time.Time{}, false},
		{"blocked since prompt", Agent{PromptAt: prompt, Report: rep("blocked", now.Add(-time.Minute))}, false, time.Time{}, false},
		{"question since prompt", Agent{PromptAt: prompt, Report: rep("question", now.Add(-time.Minute))}, false, time.Time{}, false},
		{"progress since prompt", Agent{PromptAt: prompt, Report: rep("progress", now.Add(-time.Minute))}, false, time.Time{}, true},
		{"done before prompt", Agent{PromptAt: prompt, Report: rep("done", prompt.Add(-time.Minute))}, false, time.Time{}, true},
		{"tool says already blocked", Agent{PromptAt: prompt}, true, time.Time{}, false},
		{"fleet blocked this prompt", Agent{PromptAt: prompt}, false, now.Add(-time.Second), false},
		{"fleet blocked an earlier prompt", Agent{PromptAt: prompt}, false, prompt.Add(-time.Hour), true},
		{"card only, blocked once", Agent{Card: &Card{}}, false, now.Add(-time.Hour), false},
		{"interactive", Agent{PromptAt: prompt, Interactive: true}, false, time.Time{}, false},
		{"user typed lately", Agent{PromptAt: prompt, UserAt: now.Add(-time.Minute)}, false, time.Time{}, false},
		{"user typed long ago", Agent{PromptAt: prompt, UserAt: now.Add(-time.Hour)}, false, time.Time{}, true},
	}
	for _, c := range cases {
		if got := c.a.BlockStop(now, c.active, c.blockedAt); got != c.want {
			t.Errorf("%s: BlockStop = %v, want %v", c.name, got, c.want)
		}
	}
	var nilAgent *Agent
	if nilAgent.BlockStop(now, false, time.Time{}) {
		t.Error("nil agent blocked")
	}
}
