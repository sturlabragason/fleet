package fleet

import (
	"fmt"

	"github.com/sturlabragason/fleet/internal/tools"
)

// pickModel is the model fleet launches an agent with, "" for a tool with
// no model flag (ADR-100, ADR-112). A named model is any id the profile's
// tool takes, at every effort and as a leader; a profile with no model flag
// refuses one. Without a named model, a model the profile's args or the
// user's extra_args name is pinned again (so an ACP adapter's model
// variable follows it), else the profile's default_model (Opus for the
// built-in Copilot and Claude profiles); a tool with no model flag, or a
// profile with no default_model, keeps the tool's own default.
func (f *Fleet) pickModel(profile string, p tools.Profile, named string) (string, error) {
	if named != "" {
		if !p.HasModelFlag() {
			return "", fmt.Errorf("profile %s can't select a model: its tool has no model flag (model_args), so it runs the tool's own default; can't run model %q", profile, named)
		}
		return named, nil
	}
	if !p.HasModelFlag() {
		return "", nil
	}
	if own := tools.ModelArg(p.Args, f.Prefs.For(profile).ExtraArgs); own != "" {
		return own, nil
	}
	return p.DefaultModel, nil
}

// AdmiralModel is p set up to run its model as an admiral: the one its args
// name, else default_model (Opus for the built-in profiles). The admiral
// takes no extra_args, so only args count. m is the model it runs, "" for
// a tool with no model flag.
func AdmiralModel(profile string, p tools.Profile) (tools.Profile, string, error) {
	if !p.HasModelFlag() {
		return p, "", nil
	}
	m := p.DefaultModel
	if own := tools.ModelArg(p.Args); own != "" {
		m = own
	}
	return p.WithModel(m), m, nil
}

// withModel is p set up for the model pickModel gives profile, for the
// launch, wake, adopt and resume paths; m is what the board records.
func (f *Fleet) withModel(profile string, p tools.Profile, named string) (tools.Profile, string, error) {
	m, err := f.pickModel(profile, p, named)
	if err != nil {
		return p, "", err
	}
	return p.WithModel(m), m, nil
}

// modelExtra drops a model from the user's extra_args when fleet picked
// one, so a model named on the spawn is the one the tool runs.
func modelExtra(p tools.Profile, model string, extra []string) []string {
	if model == "" {
		return extra
	}
	return p.StripModel(extra)
}
