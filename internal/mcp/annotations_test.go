package mcp

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
)

// Every built-in tool has a title and hints (ADR-212); fleet_wait a title only.
func TestToolMetasCoverEveryTool(t *testing.T) {
	for _, tl := range slices.Concat(toolDefs, agentToolDefs, squadToolDefs) {
		if _, ext := extTools[tl.Name]; ext {
			continue // an extension may list without an entry
		}
		m, ok := toolMetas[tl.Name]
		switch {
		case !ok || m.title == "":
			t.Errorf("%s has no entry in toolMetas (annotations.go): give it a title and hints", tl.Name)
		case tl.Name == "fleet_wait" && m.hints != nil:
			t.Errorf("fleet_wait must carry no hints: a parallel call ends it (ADR-72)")
		case tl.Name != "fleet_wait" && m.hints == nil:
			t.Errorf("%s has a title but no hints", tl.Name)
		}
	}
}

func TestToolsListCarriesAnnotations(t *testing.T) {
	res, rerr := (&Server{}).handle(request{Method: "tools/list"})
	if rerr != nil {
		t.Fatal(rerr)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	type listed struct {
		Name        string          `json:"name"`
		Title       string          `json:"title"`
		Annotations *map[string]any `json:"annotations"`
	}
	var out struct{ Tools []listed }
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, title string
		hints       map[string]any // nil: no annotations
	}{
		{"fleet_status", "Fleet status", map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false}},
		{"fleet_close", "Close agent", map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": true}},
		{"fleet_wait", "Wait for mail", nil},
	}
	for _, tt := range tests {
		i := slices.IndexFunc(out.Tools, func(x listed) bool { return x.Name == tt.name })
		if i < 0 {
			t.Errorf("%s not listed", tt.name)
			continue
		}
		got := out.Tools[i]
		if got.Title != tt.title {
			t.Errorf("%s title %q, want %q", tt.name, got.Title, tt.title)
		}
		switch {
		case tt.hints == nil && got.Annotations != nil:
			t.Errorf("%s annotations %v, want none", tt.name, *got.Annotations)
		case tt.hints != nil && (got.Annotations == nil || !maps.Equal(*got.Annotations, tt.hints)):
			t.Errorf("%s annotations %v, want %v", tt.name, got.Annotations, tt.hints)
		}
	}
}

// A tool without an entry (an extension's) lists as before.
func TestToolWithoutMetaListsUnchanged(t *testing.T) {
	b, err := json.Marshal(tool{"x_ext", "d", obj(nil, nil)})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"name":"x_ext","description":"d","inputSchema":{"properties":null,"type":"object"}}`; string(b) != want {
		t.Errorf("got %s, want %s", b, want)
	}
}
