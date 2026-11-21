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
		Config: a.Config, Check: a.Card != nil && a.Card.Check != "", Exit: exitClass(a.Exit)}
	repo := a.Dir
	if wt := a.Worktree; wt != nil {
		rec.Branch = wt.Branch
		if wt.Repo != "" {
			repo = wt.Repo
		}
	}
	rec.Repo = filepath.Base(repo)
	if ms, err := f.Mail().InboxFrom(box, a.Name, true, math.MaxInt32); err == nil {
		rec.Reports, rec.Last, rec.FirstDone, rec.HasRetro = tallyReports(ms, a.Created)
	}
	if evs, err := f.Store.ReadEvents(a.Created); err == nil {
		tallyEvents(&rec, evs, now)
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

// exitClass is how the run ended: clean (its process exited 0), crash (any
// other exit or signal) or closed (fleet closed it with the process alive).
func exitClass(e *board.Exit) string {
	switch {
	case e == nil:
		return "closed"
	case e.Clean():
		return "clean"
	}
	return "crash"
}

// tallyReports counts the reports filed since start by state, and returns
// the last state, the time of the first done and whether a report had a
// retro: line.
func tallyReports(ms []mail.Msg, start time.Time) (by map[string]int, last string, firstDone time.Time, retro bool) {
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
		retro = retro || hasRetro(m.Text)
	}
	return by, last, firstDone, retro
}

// hasRetro is whether a report has a "retro:" detail line.
func hasRetro(text string) bool {
	for l := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(strings.TrimPrefix(strings.TrimSpace(l), "- "), "retro:") {
			return true
		}
	}
	return false
}

// tallyEvents fills rec from the events of its agent in one pass (ADR-170,
// ADR-187): the last done-check verdict, the friction kinds into their own
// counters, other interventions by kind, and the seconds spent in each
// status, the last one up to closed.
func tallyEvents(rec *runs.Record, evs []board.Event, closed time.Time) {
	var state string
	var since time.Time
	for _, e := range evs {
		if e.Agent != rec.Name {
			continue
		}
		switch e.Kind {
		case "":
			if e.To != "" {
				addSecs(&rec.TimeS, state, since, e.Time)
				state, since = e.To, e.Time
			}
		case board.KindDoneCheck:
			rec.CheckResult = e.Detail
		case board.KindDoneRefused:
			inc(&rec.DoneRefusals, e.Detail)
			rec.DoneBounces++
		case board.KindDeliver:
			inc(&rec.Sends, e.Detail)
		case board.KindTakeover:
			inc(&rec.Sends, board.KindTakeover)
		case board.KindCrash: // the board's Exit says it already (exitClass)
		default:
			inc(&rec.Interventions, e.Kind)
		}
	}
	addSecs(&rec.TimeS, state, since, closed)
}

func inc(m *map[string]int, k string) {
	if *m == nil {
		*m = map[string]int{}
	}
	(*m)[k]++
}

// addSecs adds the whole seconds from since to until to (*m)[state]; no
// state (before the first transition) adds nothing.
func addSecs(m *map[string]int, state string, since, until time.Time) {
	if state == "" || !until.After(since) {
		return
	}
	if *m == nil {
		*m = map[string]int{}
	}
	(*m)[state] += int(until.Sub(since).Seconds())
}
