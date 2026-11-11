package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// ADR-112: a model the session already runs costs no call; another one is
// switched to with the method the agent's result implies, and a refusal is
// an error naming what the agent offers.
func TestUseModel(t *testing.T) {
	cases := []struct {
		name, result, model, wantMethod, wantParams string
		refuse                                      bool
	}{
		{"current", `{"sessionId":"s1","models":{"currentModelId":"a","availableModels":[{"modelId":"a"},{"modelId":"b"}]}}`, "a", "", "", false},
		{"set_model", `{"sessionId":"s1","models":{"currentModelId":"a","availableModels":[{"modelId":"a"},{"modelId":"b"}]}}`, "b",
			MethodSessionSetModel, `{"sessionId":"s1","modelId":"b"}`, false},
		{"set_model refused", `{"sessionId":"s1","models":{"currentModelId":"a","availableModels":[{"modelId":"a"},{"modelId":"b"}]}}`, "zz",
			MethodSessionSetModel, `{"sessionId":"s1","modelId":"zz"}`, true},
		{"config option", `{"sessionId":"s1","configOptions":[{"id":"mode","category":"mode","type":"select","currentValue":"x","options":[{"value":"x"}]},{"id":"model","category":"model","type":"select","currentValue":"opus","options":[{"value":"opus"},{"value":"sonnet"}]}]}`, "sonnet",
			MethodSessionSetConfig, `{"sessionId":"s1","configId":"model","value":"sonnet"}`, false},
		{"says nothing", `{"sessionId":"s1"}`, "whatever", "", "", false},
		{"odd shapes never fail the session", `{"sessionId":"s1","configOptions":[{"id":"fast","type":"boolean","currentValue":true,"options":[true,false]}]}`, "x", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl, p := newPair(t, &recHandler{})
			idc := make(chan error, 1)
			go func() { _, err := cl.NewSession(context.Background(), "/", nil); idc <- err }()
			m := p.recv()
			p.reply(m.ID, c.result)
			if err := <-idc; err != nil {
				t.Fatalf("session/new: %v", err)
			}
			errc := make(chan error, 1)
			go func() { errc <- cl.UseModel(context.Background(), "s1", c.model) }()
			if c.wantMethod != "" {
				m := p.recv()
				if m.Method != c.wantMethod || string(m.Params) != c.wantParams {
					t.Fatalf("got %s %s", m.Method, m.Params)
				}
				if c.refuse {
					p.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32602,"message":"Invalid modelId"}}`+"\n", m.ID))
				} else {
					p.reply(m.ID, `{}`)
				}
			}
			err := <-errc
			if (err != nil) != c.refuse {
				t.Fatalf("UseModel = %v, refuse %v", err, c.refuse)
			}
			if c.refuse && !strings.Contains(err.Error(), "a, b") {
				t.Errorf("refusal doesn't list the models: %v", err)
			}
			if !c.refuse && c.wantMethod != "" && cl.Models().Current != c.model {
				t.Errorf("current %q", cl.Models().Current)
			}
		})
	}
}

// A load or resume result's models replace the last session's.
func TestModelsFromLoad(t *testing.T) {
	cl, p := newPair(t, &recHandler{})
	errc := make(chan error, 1)
	go func() { errc <- cl.LoadSession(context.Background(), "s1", "/", nil) }()
	m := p.recv()
	p.reply(m.ID, `{"models":{"currentModelId":"x","availableModels":[{"modelId":"x"},{"modelId":"x"},{"modelId":"y"}]}}`)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	got := cl.Models()
	if b, _ := json.Marshal(got.IDs()); string(b) != `["x","y"]` || !got.Has("y") || got.Has("z") {
		t.Errorf("models %+v", got)
	}
}
