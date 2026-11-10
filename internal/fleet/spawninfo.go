package fleet

import (
	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/tools"
)

// Facts a spawn reply states (DESIGN §6.9, fleet#31): which model the
// profile runs and where a queued agent stands.

// Model is the model a profile launches with: the last --model in its args
// or in the user's extra_args for it, else the default_model fleet pins
// (ADR-100); "" when none applies and the tool picks its own default.
func (f *Fleet) Model(profile string) string {
	p, ok := f.Profiles[profile]
	if !ok {
		return ""
	}
	if m := tools.ModelArg(p.Args, f.Prefs.For(profile).ExtraArgs); m != "" || !p.HasModelFlag() {
		return m
	}
	return p.DefaultModel
}

// QueueAhead is how many queued agents start before name.
func (f *Fleet) QueueAhead(name string) int {
	for i, a := range QueueOrder(f.Store.Load().Sorted()) {
		if a.Name == name {
			return i
		}
	}
	return 0
}

// AgentModel is the model agent a runs: the one fleet started it with or
// its spawn named (ADR-100), else its profile's.
func (f *Fleet) AgentModel(a *board.Agent) string {
	if a.Model != "" {
		return a.Model
	}
	return f.Model(a.Profile)
}

// SpawnLabel is "<profile>, model <model>", or "<profile>, tool default
// model" when nothing sets one.
func (f *Fleet) SpawnLabel(a *board.Agent) string {
	if m := f.AgentModel(a); m != "" {
		return a.Profile + ", model " + m
	}
	return a.Profile + ", tool default model"
}
