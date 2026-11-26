package fleet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/plan"
	"github.com/sturlabragason/fleet/internal/tools"
)

// Plan stages recorded on a plan agent's card, and so on its run (ADR-203).
const (
	StageWrite  = "write"
	StageReview = "review"
)

// planGrace is how long a plan waits for an agent it started to show up on
// the board before it calls the task blocked.
var planGrace = 2 * time.Minute

// RunPlan starts plan p for admiral ("" = main), or resumes it from its
// state on disk (ADR-203): it starts every task whose dependencies are done.
// A plan that already has state with other tasks is refused.
func (f *Fleet) RunPlan(p *plan.Plan, admiral string) (*plan.State, error) {
	if err := f.checkPlan(p); err != nil {
		return nil, err
	}
	err := plan.Update(f.Home, p.Name, func(s *plan.State) (*plan.State, error) {
		if s == nil {
			return plan.New(p, admiral, time.Now()), nil
		}
		if !reflect.DeepEqual(s.Plan, p) {
			return nil, fmt.Errorf("plan %s already ran with other tasks; delete %s/%s.json to start it over", p.Name, plan.Dir(f.Home), p.Name)
		}
		return nil, nil
	})
	if err != nil {
		return nil, err
	}
	f.tickPlan(p.Name)
	return plan.Load(f.Home, p.Name)
}

// checkPlan refuses unknown profiles and a reviewer of the writer's own
// vendor: a review is only worth its cost from another tool family. The
// fake agent has no vendor, so it may review itself (tests).
func (f *Fleet) checkPlan(p *plan.Plan) error {
	for _, t := range p.Tasks {
		w, ok := f.Profiles[t.Profile]
		if !ok {
			return fmt.Errorf("task %s: unknown profile %q (have %v)", t.Name, t.Profile, tools.Names(f.Profiles))
		}
		if t.Review == nil {
			continue
		}
		r, ok := f.Profiles[t.Review.Profile]
		if !ok {
			return fmt.Errorf("task %s: unknown review profile %q", t.Name, t.Review.Profile)
		}
		if w.Family() == r.Family() && w.Family() != tools.FamilyFake {
			return fmt.Errorf("task %s: reviewer %s is the same vendor (%s) as writer %s; pick a profile of another tool family",
				t.Name, t.Review.Profile, r.Family(), t.Profile)
		}
	}
	return nil
}

// PlanTick advances every unfinished plan. Report and the done-check call
// it, so a plan moves on as soon as a stage passes.
func (f *Fleet) PlanTick() {
	for _, n := range plan.List(f.Home) {
		f.tickPlan(n)
	}
}

// tickPlan decides under the plan's lock and acts after it, so two ticks
// never start the same stage and an action that reports does not deadlock.
func (f *Fleet) tickPlan(name string) {
	var acts []func()
	err := plan.Update(f.Home, name, func(s *plan.State) (*plan.State, error) {
		if s == nil || s.Finished() {
			return nil, nil
		}
		before, _ := json.Marshal(s.Tasks)
		acts = f.advance(s, f.Store.Load(), time.Now())
		if after, _ := json.Marshal(s.Tasks); bytes.Equal(before, after) {
			return nil, nil
		}
		return s, nil
	})
	if err != nil {
		logx.For("plan").Warn("plan not advanced", "plan", name, "err", err)
		return
	}
	for _, act := range acts {
		act()
	}
}

// advance moves each task of s one stage on where its agent has passed,
// then starts the tasks that became ready, and returns what to do.
func (f *Fleet) advance(s *plan.State, b *board.Board, now time.Time) []func() {
	var acts []func()
	for i := range s.Plan.Tasks {
		t, ts := s.Plan.Tasks[i], s.Tasks[s.Plan.Tasks[i].Name]
		var act func()
		switch ts.Phase {
		case plan.Write, plan.Merge:
			act = f.advanceWriter(s, t, ts, b, now)
		case plan.Reviewing:
			act = f.advanceReview(s, t, ts, b, now)
		}
		if act != nil {
			acts = append(acts, act)
		}
	}
	for _, n := range s.Ready() {
		t, ts := *s.Plan.Task(n), s.Tasks[n]
		*ts = plan.TaskState{Phase: plan.Write, Round: 1, Since: now}
		acts = append(acts, func() { f.planSpawn(s, t, plan.Write, t.Name, t.Profile, t.Dir, writerPrompt(t)) })
	}
	return acts
}

// advanceWriter handles a writer that wrote (or merged) and passed.
func (f *Fleet) advanceWriter(s *plan.State, t plan.Task, ts *plan.TaskState, b *board.Board, now time.Time) func() {
	a, gone := planAgent(b, t.Name, ts.Since, now)
	switch {
	case gone != "":
		return f.planBlock(s, t, ts, gone)
	case a == nil || !stagePassed(a, ts.Since):
		return nil
	case ts.Phase == plan.Write && t.Review != nil:
		ts.Phase, ts.Since, ts.Note = plan.Reviewing, now, ""
		return func() {
			f.planSpawn(s, t, plan.Reviewing, t.Reviewer(), t.Review.Profile, a.Dir, reviewerPrompt(t, a, ts.Round))
		}
	}
	ts.Phase, ts.Since, ts.Note = plan.Done, now, ""
	return nil
}

// advanceReview reads a reviewer's verdict: pass sends the writer to merge,
// findings back to work, and findings past the round cap block the task.
func (f *Fleet) advanceReview(s *plan.State, t plan.Task, ts *plan.TaskState, b *board.Board, now time.Time) func() {
	r, gone := planAgent(b, t.Reviewer(), ts.Since, now)
	if gone != "" {
		return f.planBlock(s, t, ts, gone)
	}
	if r == nil || !doneSince(r, ts.Since) {
		return nil
	}
	text := f.lastReport(t.Reviewer(), r.Report.Summary)
	closeRev := func() {
		if err := f.closeLater(t.Reviewer()); err != nil {
			logx.For("plan").Warn("reviewer not closed", "agent", t.Reviewer(), "err", err)
		}
	}
	switch {
	case passVerdict(text) && b.Agents[t.Name] == nil:
		// The writer closed after its stage, which AgentBlocker allows only with nothing left to merge.
		ts.Phase, ts.Since, ts.Note = plan.Done, now, "review passed"
		return closeRev
	case passVerdict(text):
		ts.Phase, ts.Since, ts.Note = plan.Merge, now, "review passed"
		return func() {
			closeRev()
			f.planRearm(t.Name, true)
			f.tell(t.Name, fmt.Sprintf("[fleet] Plan %s: the review of %s passed. Merge your branch into local main now (fleet merge), then report done again.", s.Plan.Name, t.Name))
		}
	case ts.Round >= t.Review.MaxRounds:
		block := f.planBlock(s, t, ts, fmt.Sprintf("review found problems after %d round(s), the cap: %s", ts.Round, text))
		return func() { closeRev(); block() }
	}
	ts.Round++
	ts.Phase, ts.Since, ts.Note = plan.Write, now, "review findings"
	return func() {
		closeRev()
		f.planRearm(t.Name, false)
		f.tell(t.Name, fmt.Sprintf("[fleet] Plan %s: the reviewer (%s) found problems in %s. Fix them, commit, and report done again for review round %d of %d:\n%s",
			s.Plan.Name, t.Review.Profile, t.Name, ts.Round, t.Review.MaxRounds, text))
	}
}

// planAgent finds a plan agent on the board, or in its closed record. gone
// says why the task cannot go on: the agent closed in this stage without
// passing it, or never showed up.
func planAgent(b *board.Board, name string, since, now time.Time) (a *board.Agent, gone string) {
	if a := b.Agents[name]; a != nil {
		return a, ""
	}
	if c, ok := b.AnyClosed(name); ok && c.At.After(since) {
		if doneSince(&c.Agent, since) {
			return &c.Agent, ""
		}
		return nil, name + " was closed before it reported done"
	}
	if now.Sub(since) > planGrace {
		return nil, name + " is not on the board"
	}
	return nil, ""
}

// doneSince is whether a reported done after since.
func doneSince(a *board.Agent, since time.Time) bool {
	return a.Report != nil && a.Report.State == "done" && a.Report.At.After(since)
}

// stagePassed is whether a reported done after since and its done-check,
// if it has one, passed.
func stagePassed(a *board.Agent, since time.Time) bool {
	if !doneSince(a, since) {
		return false
	}
	return a.Card == nil || a.Card.Check == "" || (a.Card.Checked != nil && a.Card.Checked.Pass)
}

// passVerdict is whether a reviewer's report says pass: its first word.
func passVerdict(text string) bool {
	w, _, _ := strings.Cut(strings.TrimSpace(strings.TrimPrefix(text, "done:")), " ")
	return strings.EqualFold(strings.Trim(w, ".:!,`*"), "pass")
}

// lastReport is the full text of name's newest report, else fallback.
func (f *Fleet) lastReport(name, fallback string) string {
	ms, err := f.Mail().InboxFrom(f.ReportBox(name), name, true, 1)
	if err != nil || len(ms) == 0 {
		return fallback
	}
	return strings.TrimSpace(strings.TrimPrefix(ms[len(ms)-1].Text, "done:"))
}

// planBlock marks a task blocked and returns the action that tells the
// plan's admiral.
func (f *Fleet) planBlock(s *plan.State, t plan.Task, ts *plan.TaskState, why string) func() {
	ts.Phase, ts.Note = plan.Blocked, firstLine(why)
	box := mail.BoxOf(s.Admiral)
	return func() {
		text := fmt.Sprintf("plan %s: task %s blocked: %s", s.Plan.Name, t.Name, why)
		logx.For("plan").Info("task blocked", "plan", s.Plan.Name, "task", t.Name, "why", firstLine(why))
		m, err := f.Mail().ReportTo(t.Name, box, "blocked", text)
		if err != nil {
			logx.For("plan").Warn("blocked task not reported", "task", t.Name, "err", err)
			return
		}
		f.wakeBox(box, m.ID, t.Name, "blocked", ReportLineTo(box, t.Name, "blocked", mail.FoldSummary(text)))
	}
}

// planSpawn starts a stage's agent; a spawn that fails blocks the task.
func (f *Fleet) planSpawn(s *plan.State, t plan.Task, ph plan.Phase, name, profile, dir, prompt string) {
	card := &board.Card{Goal: firstLine(t.Prompt), Check: t.Check, Stage: StageWrite}
	o := SpawnOpts{Card: card, Role: Role{Admiral: s.Admiral}}
	if t.Review != nil {
		card.May = []string{ReviewOnly}
	}
	if ph == plan.Reviewing {
		no := false
		*card = board.Card{Goal: "review " + t.Name + " (plan " + s.Plan.Name + ")", Stage: StageReview}
		o.Worktree, o.Effort = &no, "light"
	}
	if _, err := f.SpawnWith(o, name, profile, dir, prompt); err != nil {
		logx.For("plan").Warn("plan spawn failed", "plan", s.Plan.Name, "agent", name, "err", err)
		f.planFail(s.Plan.Name, t, "spawn "+name+": "+err.Error())
	}
}

// planFail blocks a task outside a tick, for an action that failed.
func (f *Fleet) planFail(name string, t plan.Task, why string) {
	var act func()
	err := plan.Update(f.Home, name, func(s *plan.State) (*plan.State, error) {
		if s == nil {
			return nil, errors.New("plan state gone")
		}
		act = f.planBlock(s, t, s.Tasks[t.Name], why)
		return s, nil
	})
	if err != nil {
		logx.For("plan").Warn("plan not updated", "plan", name, "err", err)
		return
	}
	act()
}

// planRearm readies a writer for its next done: its done-check runs again,
// and after a passed review it may merge.
func (f *Fleet) planRearm(name string, merge bool) {
	err := f.Store.Update(func(b *board.Board) error {
		a := b.Agents[name]
		if a == nil || a.Card == nil {
			return nil
		}
		a.Card.Checked, a.Card.Started = nil, time.Time{}
		if merge {
			a.Card.May = append(slices.DeleteFunc(a.Card.May, func(m string) bool { return m == ReviewOnly }), "merge-local")
		}
		return nil
	})
	if err != nil {
		logx.For("plan").Warn("writer not re-armed", "agent", name, "err", err)
	}
}

func writerPrompt(t plan.Task) string {
	if t.Review == nil {
		return t.Prompt
	}
	return t.Prompt + "\n\nReview stage: when you report done, a reviewer on another vendor (" + t.Review.Profile +
		") reads your committed branch first. Commit, do not merge, and report done; fleet sends you the verdict here."
}

func reviewerPrompt(t plan.Task, w *board.Agent, round int) string {
	branch := "the writer's branch"
	if w.Worktree != nil && w.Worktree.Branch != "" {
		branch = "branch " + w.Worktree.Branch
	}
	return fmt.Sprintf("You review task %s (round %d of %d), written by agent %s (%s) on %s in this directory. Its task was:\n\n%s\n\n"+
		"You are read-only: do not edit, commit, merge or push anything. Read the diff against main (git diff main...HEAD) and look for bugs that break behaviour, not style; cite file:line for each finding. "+
		"Then report done. The first word of the report is the verdict: `pass` when nothing must change, otherwise `findings:` followed by the findings.",
		t.Name, round, t.Review.MaxRounds, w.Name, w.Profile, branch, t.Prompt)
}

// StartPlan parses plan text called name, keeps it as plans/<name>.toml
// and runs it for admiral; task dirs must be absolute or start with ~/.
// fleet_spawn plan=... writes plans this way.
func (f *Fleet) StartPlan(name, text, admiral string) (*plan.State, error) {
	p, err := plan.Parse(name, text)
	if err != nil {
		return nil, err
	}
	if err := p.ResolveDirs(""); err != nil {
		return nil, err
	}
	st, err := f.RunPlan(p, admiral)
	if err != nil {
		return nil, err
	}
	// Written only once RunPlan accepted it, so a refused plan never replaces a running one's file.
	if err := os.WriteFile(filepath.Join(plan.Dir(f.Home), name+".toml"), []byte(text), 0o644); err != nil {
		return nil, fmt.Errorf("write plan %s: %w", name, err)
	}
	return st, nil
}

// PlanStatus is one plan's task summary, or every plan's when name is "".
func (f *Fleet) PlanStatus(name string) (string, error) {
	names := []string{name}
	if name == "" {
		names = plan.List(f.Home)
	}
	if len(names) == 0 {
		return "no plans (fleet run <plan.toml>)", nil
	}
	var out []string
	for _, n := range names {
		s, err := plan.Load(f.Home, n)
		if err != nil {
			return "", err
		}
		out = append(out, s.Summary())
	}
	return strings.Join(out, "\n"), nil
}
