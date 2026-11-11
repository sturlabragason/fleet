package fleet

import (
	"fmt"
	"os"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/status"
)

// ClosedError is the refusal for a send to an agent that closed recently
// (ADR-95): it names when, and how to resume it. nil when name is on the
// board or has no closed record. how is "pass resume=true" or the CLI's
// "use fleet send --resume".
func (f *Fleet) ClosedError(name, how string) error {
	b := f.Store.Load()
	if _, ok := b.Agents[name]; ok {
		return nil
	}
	c, ok := b.ClosedRecord(name, time.Now())
	if !ok {
		return nil
	}
	return fmt.Errorf("%s closed at %s; %s to resume its last session", name, closedAt(c.At, time.Now()), how)
}

// closedAt is "15:04", with the date when it was not today.
func closedAt(t, now time.Time) string {
	if y, m, d := t.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}

// Resume brings a recently closed agent back on its last session, with the
// card, owner, team, squad lead, profile, driver and dir it closed with,
// and hands it text once the tool is up (ADR-95). Over max_working or low
// memory it is queued instead and text waits for its start (held=true).
// A worktree that was removed is never re-created: that is an error.
func (f *Fleet) Resume(name, text string) (a *board.Agent, held bool, err error) {
	b := f.Store.Load()
	if _, ok := b.Agents[name]; ok || f.paneOf(name) != "" {
		return nil, false, fmt.Errorf("agent %q is on the board; nothing to resume", name)
	}
	c, ok := b.ClosedRecord(name, time.Now())
	if !ok {
		return nil, false, fmt.Errorf("no record of a closed agent %q (fleet keeps them %s); find its session with fleet sessions and adopt it", name, board.ClosedKeep)
	}
	rec := c.Agent
	if rec.SessionID == "" {
		return nil, false, fmt.Errorf("%s closed with no session id recorded; nothing to resume", name)
	}
	if err := resumeDirs(&rec); err != nil {
		return nil, false, err
	}
	liveLead(b, &rec)
	q, err := f.enqueueWith(func(now time.Time, note string) *board.Agent {
		return &board.Agent{Name: name, Profile: rec.Profile, Patterns: f.Profiles[rec.Profile].Patterns, Dir: rec.Dir,
			Goal: text, SessionID: rec.SessionID, Status: string(status.Queued), Created: now, StatusSince: now, LastLine: note,
			Baseline: rec.Baseline, Worktree: rec.Worktree, NoWorktree: rec.NoWorktree, Card: rec.Card, Leader: rec.Leader,
			LeadBy: rec.LeadBy, Admiral: rec.Admiral, Effort: rec.Effort, Interactive: rec.Interactive, Driver: rec.Driver, Model: rec.Model}
	})
	if err != nil {
		return nil, false, err
	}
	if q != nil {
		f.rejoin(name, c.Team)
		return q, true, nil
	}
	a, err = f.adopt(resumeOpts(&rec), &rec, false, text)
	if err != nil {
		return nil, false, err
	}
	f.rejoin(name, c.Team)
	logx.For("fleet").Info("resumed", "agent", name, "session_id", rec.SessionID, "closed_at", c.At)
	return a, false, nil
}

// resumeQueued starts a resume the queue drain claimed. Its held text is
// the entry's goal; the agent gets its old goal back from the record.
func (f *Fleet) resumeQueued(a *board.Agent) error {
	rec, text := *a, a.Goal
	if err := resumeDirs(&rec); err != nil {
		return err
	}
	b := f.Store.Load()
	rec.Goal = ""
	if c, ok := b.AnyClosed(rec.Name); ok {
		rec.Goal = c.Agent.Goal
	}
	liveLead(b, &rec)
	_, err := f.adopt(resumeOpts(&rec), &rec, true, text)
	return err
}

// liveLead drops a squad lead whose leader is gone: the agent reports to
// the admiral.
func liveLead(b *board.Board, rec *board.Agent) {
	if _, ok := b.Agents[rec.LeadBy]; rec.LeadBy != "" && !ok {
		rec.LeadBy = ""
	}
}

func resumeOpts(rec *board.Agent) AdoptOpts {
	return AdoptOpts{Profile: rec.Profile, SessionID: rec.SessionID, Dir: rec.Dir, Name: rec.Name, Admiral: rec.Admiral, Driver: queuedDriver(rec)}
}

// resumeDirs refuses a resume whose worktree or dir is gone.
func resumeDirs(rec *board.Agent) error {
	if w := rec.Worktree; w != nil && w.Path != "" {
		if st, err := os.Stat(w.Path); err != nil || !st.IsDir() {
			return fmt.Errorf("%s's worktree %s is gone and resume does not re-create it; spawn a new agent instead", rec.Name, w.Path)
		}
	}
	if st, err := os.Stat(rec.Dir); err != nil || !st.IsDir() {
		return fmt.Errorf("%s's dir %s is gone; nothing to resume in", rec.Name, rec.Dir)
	}
	return nil
}

// rejoin puts a resumed agent back in the team it closed in, if that team
// still exists.
func (f *Fleet) rejoin(name, team string) {
	if team == "" || f.Teams == nil {
		return
	}
	if _, err := f.Teams.Members(team); err != nil {
		return
	}
	if _, err := f.Teams.Add(team, name); err != nil {
		logx.For("fleet").Warn("resume: rejoin team", "agent", name, "team", team, "err", err)
	}
}
