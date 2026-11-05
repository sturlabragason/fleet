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
