package tools

import "strings"

// ModelArg is the model a launch argv selects: the value of the last
// --model (or --model=X) across the arg lists, in launch order, so a later
// list (the user's extra_args) wins. "" = none set, the tool's own default.
func ModelArg(lists ...[]string) string {
	m := ""
	for _, args := range lists {
		for i, a := range args {
			if v, ok := strings.CutPrefix(a, "--model="); ok {
				m = v
			} else if a == "--model" && i+1 < len(args) {
				m = args[i+1]
			}
		}
	}
	return m
}

// IsOpus reports whether model names an Opus model ("opus",
// "claude-opus-5.5"): the only family a fleet agent runs (ADR-100).
func IsOpus(model string) bool { return strings.Contains(strings.ToLower(model), "opus") }

// HasModelFlag reports whether the profile's tool takes a model on its
// command line (model_args), so fleet can pin one (ADR-100).
func (p Profile) HasModelFlag() bool { return len(p.ModelArgs) > 0 }

// modelFlag is the flag model_args selects a model with ("--model").
func (p Profile) modelFlag() string {
	if len(p.ModelArgs) == 0 {
		return ""
	}
	return p.ModelArgs[0]
}

// StripModel returns args without the profile's model flag and its value
// (both "--model X" and "--model=X"), so a model fleet picks is the only one.
func (p Profile) StripModel(args []string) []string {
	flag := p.modelFlag()
	if flag == "" || !strings.HasPrefix(flag, "-") {
		return args
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == flag:
			i++ // and its value
		case strings.HasPrefix(args[i], flag+"="):
		default:
			out = append(out, args[i])
		}
	}
	return out
}

// WithModel returns the profile set to launch model m (ADR-100): its own
// model flag dropped from args and model_args appended with {model} = m,
// and under an ACP adapter the acp_model_env variable set to m. "" or a
// profile with no model flag returns p unchanged.
func (p Profile) WithModel(m string) Profile {
	if m == "" || !p.HasModelFlag() {
		return p
	}
	args := p.StripModel(p.Args)
	for _, a := range p.ModelArgs {
		args = append(args, strings.ReplaceAll(a, "{model}", m))
	}
	p.Args = args
	if p.ACPModelEnv != "" {
		env := make(map[string]string, len(p.ACPEnvVars)+1)
		for k, v := range p.ACPEnvVars {
			env[k] = v
		}
		env[p.ACPModelEnv] = m
		p.ACPEnvVars = env
	}
	return p
}
