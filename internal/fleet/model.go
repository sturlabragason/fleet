package fleet

import (
	"fmt"

	"github.com/sturlabragason/fleet/internal/tools"
)

// pickModel is the model fleet launches an agent with (ADR-100), "" for a
// tool with no model flag. Every
// fleet agent runs Opus, at every effort and as a leader: a named model
// must be Opus, a model the profile's args or extra_args name must be Opus
// and is pinned again (so an ACP adapter's model variable follows it), and
// a profile that names none is pinned to its default_model. A tool with no
// model flag keeps its own default.
func (f *Fleet) pickModel(profile string, p tools.Profile, named string) (string, error) {
	if named != "" {
		if !p.HasModelFlag() {
			return "", fmt.Errorf("profile %s has no model flag (model_args); can't run model %q", profile, named)
		}
		if !tools.IsOpus(named) {
			return "", fmt.Errorf("model %q refused: fleet agents run Opus at every effort (ADR-100); subagents may use Sonnet for scoped, checkable subtasks", named)
		}
		return named, nil
	}
	if !p.HasModelFlag() {
		return "", nil
	}
	if own := tools.ModelArg(p.Args, f.Prefs.For(profile).ExtraArgs); own != "" {
		if !tools.IsOpus(own) {
			return "", fmt.Errorf("profile %s runs model %q: fleet agents run Opus at every effort (ADR-100); set an Opus model in its args or extra_args", profile, own)
		}
		return own, nil
	}
	return p.DefaultModel, nil
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
