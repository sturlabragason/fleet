package fleet

import (
	"sort"

	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/spend"
)

// AgentSpend is one row of the spend report.
type AgentSpend struct {
	Name    string `json:"name"`
	Profile string `json:"profile"`
	Session string `json:"session,omitempty"`
	Known   bool   `json:"known"` // the tool records usage and the file was read
	spend.Spend
}

// Spend returns what one agent has cost so far, read from its own session
// file. ok is false when its tool records no usage fleet can read.
func (f *Fleet) Spend(name string) (spend.Spend, bool) {
	a, ok := f.Store.Load().Agents[name]
	if !ok {
		return spend.Spend{}, false
	}
	return f.SpendOf(a)
}

// SpendOf reads one agent's usage from its tool's session file or, for an
// ACP agent whose tool keeps none fleet can read (agy), from the usage the
// host recorded in its transcript (DESIGN §7d).
func (f *Fleet) SpendOf(a *board.Agent) (spend.Spend, bool) {
	p := f.Profiles[a.Profile]
	if path, _ := spend.Source(p, a.SessionID); path == "" && a.ACP() {
		return spend.Default.File(acphost.TranscriptPath(f.Home, a.Name), spend.ACP)
	}
	return spend.Default.Agent(p, a.SessionID)
}

// SpendAll reads every agent on the board, sorted by name, and the
// fleet-wide total. It reads files only, never a model, and each file only
// where it grew since the last call.
func (f *Fleet) SpendAll() (rows []AgentSpend, total spend.Spend) {
	for _, a := range f.Store.Load().Agents {
		r := AgentSpend{Name: a.Name, Profile: a.Profile, Session: a.SessionID}
		if s, ok := f.SpendOf(a); ok {
			r.Spend, r.Known = s, true
			total.Add(s)
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, total
}

// SpendNote explains what the figures leave out. The summariser (ADR-40)
// shells out to a fresh, anonymous CLI session per call, so its tokens are
// recorded in ~/.copilot/session-state under an id fleet never learns and
// cannot be attributed to an agent or to the fleet.
const SpendNote = "agent sessions only; summariser calls (fleet peek --summary) are not attributable"
