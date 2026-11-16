package fleet

import (
	"fmt"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/status"
)

// Intervene records in events.jsonl that fleet's own policy acted on agent
// name (ADR-154). An agent no longer on the board is recorded by name.
func (f *Fleet) Intervene(name, kind, detail string) {
	a, ok := f.Store.Load().Agents[name]
	if !ok {
		a = &board.Agent{Name: name}
	}
	f.Store.AppendEvent(board.Intervention(time.Now(), a, kind, detail))
}

// noteDisagree records a status_disagree when a hook overrides a screen
// that matched a pattern with a different status: both values and the rule
// that matched, so pattern hits can be counted. A disagreement is recorded
// once until it changes or ends, not on every poll, and only by the
// housekeeping process, so one-shot commands add no lines.
func (f *Fleet) noteDisagree(a *board.Agent, hook, screen status.Status, r status.Rule, differ bool, now time.Time) {
	if !f.Housekeeping {
		return
	}
	if !differ {
		delete(f.disagree, a.Name)
		return
	}
	detail := fmt.Sprintf("hook=%s screen=%s", hook, screen)
	key := fmt.Sprintf("%s %s %d", detail, r.Kind, r.Index)
	if f.disagree[a.Name] == key {
		return
	}
	if f.disagree == nil {
		f.disagree = map[string]string{}
	}
	f.disagree[a.Name] = key
	e := board.Intervention(now, a, board.KindStatusDisagree, detail)
	e.Pattern = &board.PatternHit{Set: a.Patterns, Kind: r.Kind, Index: r.Index}
	f.Store.AppendEvent(e)
}
