package mcp

import "encoding/json"

// toolHints are MCP ToolAnnotations (ADR-212). All four are always sent:
// the spec's defaults (destructive and open-world true) are the opposite of
// most fleet tools.
type toolHints struct {
	ReadOnly    bool `json:"readOnlyHint"`
	Destructive bool `json:"destructiveHint"`
	Idempotent  bool `json:"idempotentHint"`
	OpenWorld   bool `json:"openWorldHint"`
}

// toolMeta is a tool's title and hints; hints nil sends the title only.
type toolMeta struct {
	title string
	hints *toolHints
}

func ro(title string) toolMeta   { return toolMeta{title, &toolHints{ReadOnly: true}} }
func rw(title string) toolMeta   { return toolMeta{title, &toolHints{}} }
func idem(title string) toolMeta { return toolMeta{title, &toolHints{Idempotent: true}} }
func destructive(title string, idempotent, openWorld bool) toolMeta {
	return toolMeta{title, &toolHints{Destructive: true, Idempotent: idempotent, OpenWorld: openWorld}}
}

// toolMetas is the side table tools/list adds to every built-in tool, kept
// apart so the positional tool literals stay as they are. An extension tool
// without an entry lists unchanged; annotations_test fails on a built-in one.
var toolMetas = map[string]toolMeta{
	// Reads: change nothing, safe to run in parallel or retry.
	"fleet_status":         ro("Fleet status"),
	"fleet_info":           ro("Agent info"),
	"fleet_profiles":       ro("Tool profiles"),
	"fleet_sessions":       ro("Outside sessions"),
	"fleet_access":         ro("Access list"),
	"fleet_touching":       ro("Touched files"),
	"fleet_changes":        ro("Fleet changes"),
	"fleet_instructions":   ro("Fleet instructions"),
	"fleet_brief":          ro("Session brief"),
	"fleet_recall":         ro("Recall a topic"),
	"fleet_admirals":       ro("List admirals"),
	"fleet_agents":         ro("List agents"),
	"fleet_squad_status":   ro("Squad status"),
	"fleet_squad_info":     ro("Squad agent info"),
	"fleet_squad_peek":     ro("Peek at squad agent"),
	"fleet_squad_profiles": ro("Squad tool profiles"),
	"fleet_squad_reports":  ro("Squad reports"),
	// summary=true calls a model: the one read that leaves the machine.
	"fleet_peek": {"Peek at agent", &toolHints{ReadOnly: true, OpenWorld: true}},

	// Writes that are not destructive.
	"fleet_spawn":            rw("Spawn agent"),
	"fleet_send":             rw("Send prompt"),
	"fleet_answer":           rw("Answer agent prompt"),
	"fleet_note":             rw("Save note"),
	"fleet_wall":             rw("Wall layout"),
	"fleet_adopt":            rw("Adopt session"),
	"fleet_takeover":         rw("Take over session"),
	"fleet_handoff":          rw("Hand off admiral"),
	"fleet_handover":         rw("Hand over agent"),
	"fleet_handover_request": rw("Request handover"),
	"fleet_admiral_add":      rw("Add admiral"),
	"fleet_admiral_profile":  rw("Switch admiral profile"),
	"fleet_adr":              rw("ADR numbers"),
	"fleet_msg_send":         rw("Send message"),
	"fleet_access_request":   rw("Request access"),
	"fleet_report":           rw("Report upward"),
	"fleet_squad_send":       rw("Send to squad agent"),
	"fleet_squad_spawn":      rw("Spawn squad agent"),
	// Marks what it returns as read: a retried call would come back empty.
	"fleet_inbox":          rw("Read inbox"),
	"fleet_reports":        rw("Read reports"),
	"fleet_access_decide":  idem("Decide access"),
	"fleet_access_respond": idem("Answer access request"),
	"fleet_access_revoke":  idem("Revoke access"),
	"fleet_park":           idem("Park or wake agent"),

	// Destructive: stop agents, close teams, delete routes, worktrees or branches.
	"fleet_admiral_close": destructive("Close admiral", true, false),
	"fleet_route":         destructive("Request routes", false, false), // rm
	"fleet_worktrees":     destructive("Agent worktrees", false, false),
	// These reach origin: a close deletes the agent's merged fleet/ branch there.
	"fleet_close":       destructive("Close agent", true, true),
	"fleet_squad_close": destructive("Close squad agent", true, true),
	"fleet_team":        destructive("Teams", false, true),
	"fleet_branches":    destructive("Fleet branches", false, true),

	// No hints: a parallel call ends a wait by design (ADR-72), so
	// readOnlyHint would invite clients to run it beside other calls.
	"fleet_wait": {title: "Wait for mail"},
}

// MarshalJSON adds the tool's title and annotations from toolMetas.
func (t tool) MarshalJSON() ([]byte, error) {
	type plain tool
	m, ok := toolMetas[t.Name]
	if !ok {
		return json.Marshal(plain(t))
	}
	return json.Marshal(struct {
		plain
		Title       string     `json:"title"`
		Annotations *toolHints `json:"annotations,omitempty"`
	}{plain(t), m.title, m.hints})
}
