package mcp

import (
	"strings"
	"testing"
)

// admiralToolBudget is the most built-in tools the admiral server may list
// (ADR-126): every tool's schema rides on every admiral request.
const admiralToolBudget = 40

// The admiral's built-in tools stay at a fixed budget, and each extension
// (one integration) adds at most one tool, grouped by an `action` enum, so a
// build's total is the budget plus one per integration. Only the public
// test-only example extension (build tag extexample) skips the enum rule.
func TestAdmiralToolBudget(t *testing.T) {
	n := len(ToolNames())
	for _, e := range exts {
		n -= len(e.Tools)
		if len(e.Tools) > 1 {
			t.Errorf("extension %s adds %d admiral tools; an integration gets one, with an `action` argument (ADR-126)", e.Name, len(e.Tools))
		}
		for _, d := range e.Tools {
			if e.Name != "example" && !hasActionEnum(d) {
				t.Errorf("extension %s: tool %s has no `action` enum argument (ADR-126)", e.Name, d.Name)
			}
		}
	}
	if n > admiralToolBudget {
		t.Errorf("the admiral has %d built-in tools, over its budget of %d (ADR-126): merge the new verb into an existing tool with an `action` argument, or retire a tool; don't raise the budget", n, admiralToolBudget)
	}
}

// An integration command points at its integration's one grouped tool,
// which exists and takes an `action` enum, as fleet_route does (ADR-126).
func TestIntegrationParity(t *testing.T) {
	defs := map[string]tool{}
	for _, d := range toolDefs {
		defs[d.Name] = d
	}
	for cmd, v := range CLIParity {
		if !strings.HasPrefix(v, integrationPrefix) {
			continue
		}
		name, ok := IntegrationTool(v)
		if !ok {
			t.Errorf("fleet %s: %q names no fleet_ tool after %q", cmd, v, integrationPrefix)
			continue
		}
		d, found := defs[name]
		if !found {
			t.Errorf("fleet %s: integration tool %s is missing from the admiral server", cmd, name)
			continue
		}
		if !hasActionEnum(d) {
			t.Errorf("fleet %s: integration tool %s has no `action` enum argument", cmd, name)
		}
	}
}

func hasActionEnum(d tool) bool {
	props, _ := d.InputSchema["properties"].(map[string]any)
	action, _ := props["action"].(map[string]any)
	enum, _ := action["enum"].([]string)
	return len(enum) > 0
}

func TestIntegrationTool(t *testing.T) {
	for v, want := range map[string]string{
		"integration: fleet_route":                   "fleet_route",
		"integration: fleet_route (list, show, set)": "fleet_route",
		"integration:  fleet_route":                  "fleet_route",
		"integration: route":                         "",
		"fleet_route":                                "",
		"user: the overview TUI in a terminal":       "",
	} {
		got, ok := IntegrationTool(v)
		if got != want || ok != (want != "") {
			t.Errorf("IntegrationTool(%q) = %q, %v; want %q", v, got, ok, want)
		}
	}
	if got := ParityTool("integration: fleet_route (list)"); got != "fleet_route" {
		t.Errorf("ParityTool(integration) = %q, want fleet_route", got)
	}
	if !hasActionEnum(routeToolDef) {
		t.Error("fleet_route, the model for a grouped integration tool, lost its action enum")
	}
}
