package mcp

import (
	"fmt"
	"strings"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
)

// Task cards in the admiral tools (DESIGN §18 #5/#6, ADR-43): fleet_spawn
// takes one, fleet_info shows it, fleet_reports ends with the done-check
// verdicts and the result files.

// card builds the spawn's task card from the tool arguments; nil when the
// call asked for none.
func (a args) card() *board.Card {
	c := board.Card{Goal: a.Goal, Check: a.Check, Result: a.Result}
	if a.CheckExit != nil {
		c.Want = *a.CheckExit
	}
	return fleet.NormalizeCard(&c)
}

// cardNote is what a fleet_spawn reply adds about the agent's task card.
func cardNote(ag *board.Agent) string {
	c := ag.Card
	if c == nil {
		return ""
	}
	out := "\ntask card: result file " + c.Result
	if c.Check != "" {
		out += fmt.Sprintf("; fleet runs `%s` in %s when %s reports done and must see exit %d", c.Check, ag.Dir, ag.Name, c.Want)
	}
	return out
}

// taskCards is the fleet_reports section that turns reports into facts: for
// every agent with a card, the done-check verdict and the result file. ""
// when no agent has one.
func taskCards(as []*board.Agent) string {
	var b strings.Builder
	for _, a := range as {
		c := a.Card
		if c == nil {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("\nTask cards (fleet's own check, not the agent's words):\n")
			b.WriteString("agent | goal | done-check | result file\n")
		}
		chk := fleet.CheckState(a)
		if chk == "" {
			chk = "none"
		}
		res := "none"
		if p, written, ok := fleet.ResultPath(a); ok {
			res = p + map[bool]string{true: " (written)", false: " (not written yet)"}[written]
		}
		fmt.Fprintf(&b, "%s | %s | %s | %s\n", a.Name, c.Goal, chk, res)
	}
	return b.String()
}
