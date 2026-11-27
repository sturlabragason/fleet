package mcp

import "github.com/sturlabragason/fleet/internal/tools"

// resetShell is the run-shell command fleet_handoff uses to replace the
// admiral (qexe: the shell-quoted fleet binary); the delay lets the tool
// result reach the admiral first.
func resetShell(qexe string, admiral ...string) string {
	n := ""
	if len(admiral) > 0 {
		n = nameFlag(admiral[0])
	}
	return tools.Silent("sleep 2; " + qexe + " admiral reset" + n + " --handoff")
}

// nameFlag is " --name X" for an admiral other than main, "" for main.
func nameFlag(admiral string) string {
	if admiral == "" || admiral == "main" {
		return ""
	}
	return " --name " + admiral
}
