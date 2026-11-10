package fleet

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/summary"
)

// PeekScreenLines is how much pane history a summary is made from.
const PeekScreenLines = 200

// Summarizer is the cheap summariser behind `fleet peek --summary` and MCP
// fleet_peek summary=true (DESIGN §6.2, ADR-40). It is built once from the
// [summary] section of config.toml. Tests set SummaryModel instead.
func (f *Fleet) Summarizer() (*summary.Summarizer, error) {
	cfg := f.Prefs.Summary()
	if !cfg.Enabled && f.SummaryModel == nil {
		return nil, fmt.Errorf("summaries are off ([summary] enabled = false)")
	}
	f.summaryMu.Lock()
	defer f.summaryMu.Unlock()
	if f.summarizer == nil {
		// Built once and never mutated afterwards, so PeekSummary is safe
		// to call from a TUI command goroutine.
		var m summary.Model = f.SummaryModel
		if m == nil {
			m = &summary.CLI{Command: cfg.Command, Args: cfg.Args, Model: cfg.Model, Timeout: cfg.Timeout}
		}
		f.summarizer = &summary.Summarizer{Model: m, Dir: filepath.Join(f.Home, "peek-cache"), MinInterval: cfg.MinInterval}
	}
	return f.summarizer, nil
}

// PeekSummary returns a few lines about what one agent is doing, what it
// waits for and any error. It fails open: when the model is unreachable the
// error says so and the caller shows raw lines instead.
func (f *Fleet) PeekSummary(ctx context.Context, name string) (summary.Result, error) {
	a, ok := f.Store.Load().Agents[name]
	if !ok {
		return summary.Result{}, fmt.Errorf("no agent %q", name)
	}
	s, err := f.Summarizer()
	if err != nil {
		return summary.Result{}, err
	}
	screen, err := f.Peek(name, PeekScreenLines)
	if err != nil {
		return summary.Result{}, err
	}
	in := summary.Input{
		Agent:  name,
		Screen: status.CleanScreen(screen),
		Status: a.Status,
		Task:   a.Goal,
	}
	// The last message is a bonus, not a requirement: profiles without a
	// session log simply do not have one (and the screen is already in).
	if msg, ok := f.loggedMessage(name); ok {
		in.LastMessage = msg
	}
	return s.Summarize(ctx, in)
}
