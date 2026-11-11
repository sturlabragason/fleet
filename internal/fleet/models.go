package fleet

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sturlabragason/fleet/internal/acp"
	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/tools"
)

// Model discovery (ADR-112). Fleet keeps no list of model ids: it asks the
// tool. A profile with an ACP server is started once, without a prompt,
// and its session/new result names the models it offers (Copilot and agy
// send "models", claude-agent-acp a "model" config option). No model is
// called, so a probe costs no tokens.

// ProfileModels is what fleet knows about one profile's models.
type ProfileModels struct {
	Profile string `json:"profile"`
	// Selectable: the profile has model_args, so a spawn may name a model.
	Selectable bool `json:"selectable"`
	// Default is the model a spawn that names none runs, "" for the tool's
	// own default.
	Default string `json:"default,omitempty"`
	// Discoverable: the tool has an ACP server fleet can ask.
	Discoverable bool `json:"discoverable"`
	// Listed: the tool answered a probe; Current and Models are its answer.
	Listed  bool            `json:"listed"`
	Current string          `json:"current,omitempty"`
	Models  []acp.ModelInfo `json:"models,omitempty"`
	// Note says why there is no list, or what the list can't tell.
	Note string `json:"note,omitempty"`
}

// ErrNoDiscovery is a probe of a profile with no ACP server.
var ErrNoDiscovery = errors.New("the tool has no ACP server to ask (no acp or acp_command in its profile)")

// ModelProbeTimeout bounds one probe: an npx adapter's first run downloads it.
var ModelProbeTimeout = 90 * time.Second

// modelCacheTTL is how long a probe's answer is reused, so a squad of
// spawns on one model starts the tool once.
const modelCacheTTL = 10 * time.Minute

type modelCache struct {
	mu sync.Mutex
	m  map[string]cachedModels
}

type cachedModels struct {
	at time.Time
	ms acp.Models
}

// ModelSupport says, without starting the tool, whether profile can select
// a model, its default, and whether its tool can be asked for its models.
func (f *Fleet) ModelSupport(profile string) (ProfileModels, error) {
	p, ok := f.Profiles[profile]
	if !ok {
		return ProfileModels{}, fmt.Errorf("unknown profile %q (have %v)", profile, tools.Names(f.Profiles))
	}
	out := ProfileModels{Profile: profile, Selectable: p.HasModelFlag(), Default: f.Model(profile), Discoverable: p.HasACP()}
	switch {
	case !out.Selectable:
		out.Note = "no model flag (model_args): fleet can't select a model, the tool runs its own default"
	case !out.Discoverable:
		out.Note = "the tool can't be asked for its models (no ACP server); fleet passes a named model to it unchecked"
	}
	return out, nil
}

// ProfileModels is ModelSupport plus what profile's tool offers, asked
// over ACP when it can be.
func (f *Fleet) ProfileModels(profile string) (ProfileModels, error) {
	out, err := f.ModelSupport(profile)
	if err != nil || !out.Discoverable {
		return out, err
	}
	p := f.Profiles[profile]
	ms, err := f.probeModels(profile, p, "")
	if err != nil {
		if out.Note == "" {
			out.Note = "asking the tool failed: " + err.Error()
		}
		return out, nil
	}
	if !ms.Known() {
		if out.Note == "" {
			out.Note = "the tool's ACP server names no models; fleet passes a named model to it unchecked"
		}
		return out, nil
	}
	out.Listed, out.Current, out.Models = true, ms.Current, dedupe(ms.Available)
	return out, nil
}

func dedupe(in []acp.ModelInfo) []acp.ModelInfo {
	seen := map[string]bool{}
	var out []acp.ModelInfo
	for _, m := range in {
		if m.ModelID != "" && !seen[m.ModelID] {
			seen[m.ModelID] = true
			out = append(out, m)
		}
	}
	return out
}

// checkModel refuses a model the profile's tool says it doesn't offer, so
// a typo never runs the tool's default instead (Copilot falls back without
// a word). The probe starts the tool with the model applied: an adapter
// that takes any id (claude-agent-acp) lists it as current. A tool that
// can't be asked gets the model unchecked, and that is logged.
func (f *Fleet) checkModel(profile string, p tools.Profile, model string) error {
	if !p.HasACP() {
		return nil
	}
	ms, err := f.probeModels(profile, p, model)
	if err != nil {
		logx.For("fleet").Warn("models: can't ask the tool; passing the model unchecked", "profile", profile, "model", model, "err", err)
		return nil
	}
	if !ms.Known() || ms.Has(model) {
		return nil
	}
	return fmt.Errorf("model %q is not one profile %s's tool offers (%s); fleet models %s lists them", model, profile, strings.Join(ms.IDs(), ", "), profile)
}

// probeModels asks profile's tool, with model applied ("" = the profile's
// own), for its models; a fresh answer is reused for modelCacheTTL.
func (f *Fleet) probeModels(profile string, p tools.Profile, model string) (acp.Models, error) {
	key := profile + "\x00" + model
	f.modelCache.mu.Lock()
	if c, ok := f.modelCache.m[key]; ok && time.Since(c.at) < modelCacheTTL {
		f.modelCache.mu.Unlock()
		return c.ms, nil
	}
	f.modelCache.mu.Unlock()
	probe := f.ModelProbe
	if probe == nil {
		probe = f.acpProbe
	}
	if model == "" {
		model, _ = f.pickModel(profile, p, "")
	}
	ms, err := probe(profile, p.WithModel(model))
	if err != nil {
		return ms, err
	}
	f.modelCache.mu.Lock()
	if f.modelCache.m == nil {
		f.modelCache.m = map[string]cachedModels{}
	}
	f.modelCache.m[key] = cachedModels{at: time.Now(), ms: ms}
	f.modelCache.mu.Unlock()
	return ms, nil
}

// acpProbe starts p's ACP server in the temp dir, opens a session, reads
// its models and stops it. No prompt is sent.
func (f *Fleet) acpProbe(profile string, p tools.Profile) (acp.Models, error) {
	if !p.HasACP() {
		return acp.Models{}, ErrNoDiscovery
	}
	if len(p.ACPEnvVars) > 0 {
		path := ""
		if p.ACPNeedsCommandPath() {
			var err error
			if path, err = commandPath(p.Command); err != nil {
				return acp.Models{}, err
			}
		}
		p.Env = p.ACPEnv(path)
	}
	argv := p.ACPArgv(tools.Vars{Fleet: f.Exe, Name: "models-probe"}, tools.Launch{})
	if len(argv) > 0 && !strings.Contains(argv[0], "/") {
		if c, err := commandPath(argv[0]); err == nil {
			argv[0] = c
		}
	}
	env := acphost.FilterEnv(append(os.Environ(), p.EnvList()...), p.ACPUnsetEnv)
	ctx, cancel := context.WithTimeout(context.Background(), ModelProbeTimeout)
	defer cancel()
	cl, _, err := acp.Start(ctx, argv, os.TempDir(), env, nil, probeHandler{})
	if err != nil {
		return acp.Models{}, err
	}
	defer cl.Close()
	res, err := cl.Initialize(ctx, false)
	if err != nil {
		return acp.Models{}, fmt.Errorf("initialize: %w", err)
	}
	id, err := cl.NewSession(ctx, os.TempDir(), nil)
	if err != nil {
		return acp.Models{}, fmt.Errorf("session/new: %w", err)
	}
	ms := cl.Models()
	if res.AgentCapabilities.CanClose() {
		cctx, ccancel := context.WithTimeout(ctx, 3*time.Second)
		_ = cl.CloseSession(cctx, id)
		ccancel()
	}
	return ms, nil
}

// probeHandler answers a probe's agent: nothing runs, so no question is
// ever allowed.
type probeHandler struct{}

func (probeHandler) SessionUpdate(acp.SessionNotification) {}
func (probeHandler) CopilotEvent(acp.CopilotEvent)         {}
func (probeHandler) RequestPermission(context.Context, acp.RequestPermissionParams) (acp.RequestPermissionResult, error) {
	return acp.CancelledOutcome(), nil
}
