package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Models is what an agent said about models in its last session/new, load
// or resume result (ADR-112): the current model, the ones it offers and how
// to switch. The zero value means the agent said nothing.
type Models struct {
	Current   string
	Available []ModelInfo
	// via is the method that switches the model ("" = none offered) and
	// configID the option it sets for MethodSessionSetConfig.
	via, configID string
}

// Known reports whether the agent named any model at all.
func (m Models) Known() bool { return m.Current != "" || len(m.Available) > 0 }

// Has reports whether id is the current model or one the agent offers.
func (m Models) Has(id string) bool {
	if id == "" {
		return false
	}
	if id == m.Current {
		return true
	}
	for _, x := range m.Available {
		if x.ModelID == id {
			return true
		}
	}
	return false
}

// IDs lists the offered model ids once each, in the agent's order.
func (m Models) IDs() []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range m.Available {
		if x.ModelID != "" && !seen[x.ModelID] {
			seen[x.ModelID] = true
			out = append(out, x.ModelID)
		}
	}
	return out
}

// models reads a session result: the "models" object when the agent sends
// one, else a select config option of category "model".
func (st SessionModelState) models() Models {
	if st.Models != nil {
		return Models{Current: st.Models.CurrentModelID, Available: st.Models.AvailableModels, via: MethodSessionSetModel}
	}
	for _, o := range st.ConfigOptions {
		if o.Category != "model" && o.ID != "model" {
			continue
		}
		m := Models{via: MethodSessionSetConfig, configID: o.ID}
		m.Current, _ = o.CurrentValue.(string)
		var cs []ConfigChoice
		_ = json.Unmarshal(o.Options, &cs) // grouped options: none listed
		for _, c := range cs {
			m.Available = append(m.Available, ModelInfo{ModelID: c.Value, Name: c.Name, Description: c.Description})
		}
		return m
	}
	return Models{}
}

// Models is what the agent said about models for the last session the
// client started, loaded or resumed.
func (cl *Client) Models() Models {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.models
}

// setModels records what a session result says about models. A result
// fleet can't read as models is no models: it never fails the session.
func (cl *Client) setModels(raw json.RawMessage) {
	var st SessionModelState
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &st); err != nil {
			st = SessionModelState{}
		}
	}
	cl.mu.Lock()
	cl.models = st.models()
	cl.mu.Unlock()
}

// UseModel makes model the session's model (ADR-112). Already current: no
// call. Otherwise it asks the agent to switch, with session/set_model or the
// model config option, and returns the agent's refusal as an error naming
// the models it offers, so a wrong id never silently runs another model.
// An agent that says nothing about models keeps what its launch flag or
// environment chose, and UseModel returns nil.
func (cl *Client) UseModel(ctx context.Context, id, model string) error {
	m := cl.Models()
	if model == "" || model == m.Current || m.via == "" {
		return nil
	}
	var err error
	if m.via == MethodSessionSetConfig {
		err = cl.c.call(ctx, MethodSessionSetConfig, SetConfigParams{SessionID: id, ConfigID: m.configID, Value: model}, nil)
	} else {
		err = cl.c.call(ctx, MethodSessionSetModel, SetModelParams{SessionID: id, ModelID: model}, nil)
	}
	if err != nil {
		return fmt.Errorf("the agent refused model %q: %w (it offers: %s)", model, err, strings.Join(m.IDs(), ", "))
	}
	cl.mu.Lock()
	cl.models.Current = model
	cl.mu.Unlock()
	return nil
}
