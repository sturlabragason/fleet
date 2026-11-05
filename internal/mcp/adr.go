package mcp

import (
	"fmt"
	"strings"

	"github.com/sturlabragason/fleet/internal/adr"
)

var adrToolDef = tool{"fleet_adr", "ADR numbers for a repo's DESIGN.md, handed out atomically so parallel agents never pick the same one (ADR-50). claim (dir, optional agent) takes the next free number and returns it - put it in the spawn prompt (\"you own ADR-NN\"); list (dir) shows numbers claimed but not yet on main; release (number) gives one back. dir is any directory in the repo, the same one you would spawn an agent in.", obj([]string{"action"}, map[string]any{
	"action": map[string]any{"type": "string", "enum": []string{"claim", "list", "release"}},
	"agent":  str("the agent the claimed number is for"),
	"number": map[string]any{"type": "integer", "description": "ADR number to release"},
	"dir":    str("a directory in the repo whose DESIGN.md holds the ADRs (absolute); required for claim and list")})}

// adrCmd runs the fleet_adr tool.
func (s *Server) adrCmd(action, agent, dir string, number int) (string, error) {
	st := adr.NewStore(s.F.Home)
	needDir := func() error {
		if dir == "" {
			return fmt.Errorf("dir is required: the repo whose DESIGN.md holds the ADRs")
		}
		return nil
	}
	switch action {
	case "claim":
		if err := needDir(); err != nil {
			return "", err
		}
		onMain, err := adr.OnMain(dir)
		if err != nil {
			return "", err
		}
		n, err := st.Claim(agent, onMain)
		if err != nil {
			return "", err
		}
		who := agent
		if who == "" {
			who = "nobody yet"
		}
		return fmt.Sprintf("ADR-%d claimed for %s. Tell the agent it owns ADR-%d; the claim frees itself when ADR-%d is on main.", n, who, n, n), nil
	case "list":
		if err := needDir(); err != nil {
			return "", err
		}
		onMain, err := adr.OnMain(dir)
		if err != nil {
			return "", err
		}
		cs, err := st.List(onMain)
		if err != nil {
			return "", err
		}
		if len(cs) == 0 {
			return "no live ADR claims", nil
		}
		var b strings.Builder
		for _, c := range cs {
			who := c.For
			if who == "" {
				who = "-"
			}
			fmt.Fprintf(&b, "ADR-%d  %s\n", c.N, who)
		}
		return strings.TrimRight(b.String(), "\n"), nil
	case "release":
		if number <= 0 {
			return "", fmt.Errorf("number is required")
		}
		if err := st.Release(number); err != nil {
			return "", err
		}
		return fmt.Sprintf("released ADR-%d", number), nil
	}
	return "", fmt.Errorf("action must be claim, list or release")
}
