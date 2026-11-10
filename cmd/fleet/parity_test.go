package main

import (
	"regexp"
	"slices"
	"testing"

	"github.com/sturlabragason/fleet/internal/mcp"
)

// Every command in the usage text has a CLIParity entry (a tool or a reason
// why the admiral needs none), and every tool it names exists (ADR-86).
func TestEveryCommandHasAdmiralParity(t *testing.T) {
	re := regexp.MustCompile(`(?m)^  fleet (?:\[[^\]]*\] )*([a-z][a-z-]*)(?: ([a-z][a-z-]*))?`)
	tools := mcp.ToolNames()
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(usage, -1) {
		cmd := m[1]
		if cmd == "admiral" && m[2] != "" {
			cmd += " " + m[2]
		}
		if seen[cmd] {
			continue
		}
		seen[cmd] = true
		v, ok := mcp.CLIParity[cmd]
		if !ok {
			t.Errorf("fleet %s: no entry in mcp.CLIParity; add its admiral tool or why it needs none", cmd)
			continue
		}
		if tool := mcp.ParityTool(v); tool != "" && !slices.Contains(tools, tool) {
			t.Errorf("fleet %s: CLIParity names %s, which the admiral server lacks", cmd, tool)
		}
	}
	if len(seen) < 30 {
		t.Fatalf("parsed only %d commands from the usage text: %v", len(seen), seen)
	}
}
