package mcp

import (
	"strings"
	"testing"
)

func TestInstructionsCarryTimeRule(t *testing.T) {
	const timeRule = "Time matters here: do not spend time that can be avoided, and the earlier a correct result is obtained, the better."
	if !strings.Contains(Instructions, timeRule) {
		t.Fatal("admiral instructions lost the time rule")
	}
	if strings.Index(Instructions, timeRule) > strings.Index(Instructions, "\n## Tools\n") {
		t.Error("time rule must come before the Tools section")
	}
}

// Process review (docs/process-review.md): the three rules the admiral acts on.
func TestInstructionsCarryProcessRules(t *testing.T) {
	for _, want := range []string{
		"Merge order comes from branch state (`fleet touching`), not from the coordinator's reply",
		"Assign one ADR number per agent in the spawn prompt before the batch starts",
		"check the agent's unread reports and inbox before closing",
	} {
		if !strings.Contains(Instructions, want) {
			t.Errorf("admiral instructions lost: %s", want)
		}
	}
}
