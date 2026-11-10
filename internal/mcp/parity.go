package mcp

import "strings"

// CLIParity maps every `fleet` command (and `fleet admiral` subcommand) to
// the admiral MCP tool that does the same, or to why there is none (ADR-86):
// "user: …" for the user's own terminal or keys, "agent: …" for what an
// agent runs in its own worktree, "infra: …" for what fleet itself runs.
// A test fails when a command in the usage text has no entry here, or a tool
// named here is missing, so a new command can't skip the admiral.
var CLIParity = map[string]string{
	"up":              "user: starts the tmux session the admiral runs in",
	"ui":              "user: the overview TUI in a terminal",
	"spawn":           "fleet_spawn",
	"adopt":           "fleet_adopt",
	"sessions":        "fleet_sessions",
	"ls":              "fleet_status",
	"info":            "fleet_info",
	"spend":           "fleet_status",
	"send":            "fleet_send",
	"peek":            "fleet_peek",
	"close":           "fleet_close",
	"park":            "fleet_park",
	"wake":            "fleet_park",
	"takeover":        "fleet_takeover",
	"team":            "fleet_team",
	"mcp":             "infra: the MCP server itself",
	"mail":            "agent: agent-to-agent mail (peer tools); the admiral reads it with fleet_access limit=N",
	"access":          "fleet_access_decide",
	"report":          "agent: an agent's report (peer tool fleet_report)",
	"reports":         "fleet_reports",
	"wait":            "fleet_wait",
	"admiral":         "fleet_admirals",
	"admiral add":     "fleet_admiral_add",
	"admiral ls":      "fleet_admirals",
	"admiral close":   "fleet_admiral_close",
	"admiral min":     "user: pane layout",
	"admiral act":     "user: the admiral pane key bindings",
	"admiral give":    "fleet_handover",
	"admiral reset":   "fleet_handoff",
	"admiral profile": "fleet_admiral_profile",
	"admiral restart": "fleet_admiral_profile",
	"note":            "fleet_note",
	"playbook":        "agent: prints the playbook fleet already prepends to every spawn prompt",
	"touching":        "fleet_touching",
	"exec":            "agent: runs a build or test from its own worktree",
	"worktrees":       "fleet_worktrees",
	"branches":        "fleet_branches",
	"adr":             "fleet_adr",
	"merge":           "agent: runs in the agent's own worktree (playbook checklist step 8); the admiral asks the agent",
	"profiles":        "fleet_profiles",
	"snap":            "user: captures status fixtures for fleet's own tests",
	"wall":            "fleet_wall",
	"keys":            "user: the hotkeys",
	"changes":         "fleet_changes",
	"version":         "fleet_changes",
	"doctor":          "user: a pre-flight check of the user's machine before fleet up",
	"watch":           "infra: the status watcher fleet up starts",
	"web":             "user: a browser view the user starts",
	"hook":            "infra: tool hooks call it",
}

// ParityTool is the admiral tool for a CLIParity value, "" for an exclusion.
func ParityTool(v string) string {
	if strings.HasPrefix(v, "fleet_") {
		return v
	}
	return ""
}

// ToolNames lists the admiral server's tools.
func ToolNames() []string {
	out := make([]string, len(toolDefs))
	for i, t := range toolDefs {
		out[i] = t.Name
	}
	return out
}
