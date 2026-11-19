package fleet

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/playbook"
	"github.com/sturlabragason/fleet/internal/routes"
	"github.com/sturlabragason/fleet/internal/runs"
)

// fingerprint is the configuration a spawn starts the agent with (ADR-170):
// hashes of the playbook text it got (pbText, "" for none) and of each layer
// in sources (from playbook.Resolve), of the repo's agent notes in dir, and
// the card's route.
func (f *Fleet) fingerprint(lvl playbook.Level, dir, pbText string, sources []string, card *board.Card) *board.RunConfig {
	c := &board.RunConfig{Playbook: runs.Hash([]byte(pbText)), AgentsMD: runs.AgentsMD(dir)}
	if len(sources) > 0 {
		c.Layers = map[string]string{"builtin:" + string(lvl): runs.Hash([]byte(playbook.Text(lvl)))}
		for _, src := range sources[1:] {
			label := "repo"
			if src == filepath.Join(f.Home, "worker.md") {
				label = "user"
			}
			b, _ := os.ReadFile(src) // Resolve just read it; a vanished file hashes as ""
			c.Layers[label] = runs.Hash(b)
		}
	}
	if card != nil && card.Route != "" {
		c.Route = card.Route
		if r, err := routes.Get(f.Home, card.Route); err == nil {
			r.Updated, r.LearnedFrom = "", "" // bookkeeping, not what the route does
			if b, err := json.Marshal(r); err == nil {
				c.Route += "@" + runs.Hash(b)
			}
		}
	}
	return c
}

// recordRun appends a closed agent's run to the ledger (ADR-170): its
// reports from box (where they were filed), its interventions and done-check
// verdict from events.jsonl, and its spend. A failure is logged; the close
// has already happened.
func (f *Fleet) recordRun(a board.Agent, box string, now time.Time) {
	rec := runs.Record{Name: a.Name, Profile: a.Profile, Model: a.Model, Effort: a.Effort, Driver: a.DriverName(),
		FleetVersion: a.FleetVersion, Interactive: a.Interactive, Leader: a.Leader, Created: a.Created, Closed: now,
		Config: a.Config, Check: a.Card != nil && a.Card.Check != "", DoneBounces: a.DoneBounces}
	repo := a.Dir
	if wt := a.Worktree; wt != nil {
		rec.Branch = wt.Branch
		if wt.Repo != "" {
			repo = wt.Repo
		}
	}
	rec.Repo = filepath.Base(repo)
	if ms, err := f.Mail().InboxFrom(box, a.Name, true, math.MaxInt32); err == nil {
		rec.Reports, rec.Last, rec.FirstDone = tallyReports(ms, a.Created)
	}
	if evs, err := f.Store.ReadEvents(a.Created); err == nil {
		rec.CheckResult, rec.Interventions = tallyEvents(evs, a.Name)
	}
	if s, ok := f.SpendOf(&a); ok {
		tok := s.Uncredited
		if !s.HasAIC {
			tok = s.Total()
		}
		rec.Spend = &runs.Spend{Calls: s.Calls, AIC: s.AIC, Tokens: tok, Compactions: s.Compactions}
	}
	if err := runs.Append(f.Home, rec); err != nil {
		logx.For("fleet").Warn("run ledger", "agent", a.Name, "err", err)
	}
}

// tallyReports counts the reports filed since start by state, and returns
// the last state and the time of the first done.
func tallyReports(ms []mail.Msg, start time.Time) (by map[string]int, last string, firstDone time.Time) {
	for _, m := range ms {
		if m.Kind != mail.KindReport || m.T.Before(start) {
			continue
		}
		st, _, _ := strings.Cut(m.Text, ":")
		if by == nil {
			by = map[string]int{}
		}
		by[st]++
		last = st
		if st == "done" && firstDone.IsZero() {
			firstDone = m.T
		}
	}
	return by, last, firstDone
}

// tallyEvents is name's last done-check verdict and its other interventions
// by kind.
func tallyEvents(evs []board.Event, name string) (check string, by map[string]int) {
	for _, e := range evs {
		if e.Agent != name || e.Kind == "" {
			continue
		}
		if e.Kind == board.KindDoneCheck {
			check = e.Detail
			continue
		}
		if by == nil {
			by = map[string]int{}
		}
		by[e.Kind]++
	}
	return check, by
}
