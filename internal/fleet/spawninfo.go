package fleet

import (
	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/tools"
)

// Facts a spawn reply states (DESIGN §6.9, fleet#31): which model the
// profile runs and where a queued agent stands.

// Model is the model a profile launches with: the last --model in its args
// or in the user's extra_args for it; "" when neither sets one and the tool
// picks its own default.
func (f *Fleet) Model(profile string) string {
	p, ok := f.Profiles[profile]
	if !ok {
		return ""
	}
	return tools.ModelArg(p.Args, f.Prefs.For(profile).ExtraArgs)
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

// SpawnLabel is "<profile>, model <model>", or "<profile>, tool default
// model" when neither the profile nor extra_args set one.
func (f *Fleet) SpawnLabel(a *board.Agent) string {
	if m := f.Model(a.Profile); m != "" {
		return a.Profile + ", model " + m
	}
	return a.Profile + ", tool default model"
}
