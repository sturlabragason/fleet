package fleet

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/status"
)

// Squads (DESIGN §6.11, ADR-53). A leader is an ordinary agent with its own
// squad: it spawns members, sees only them, and their reports go to it
// instead of to the admiral. The hierarchy lives on the board — Leader on
// the leader, LeadBy on each member — and the squad is mirrored into a team
// of the leader's name, so the colour, the wall and the board show it and
// the two sides may message each other without a grant.

// Role is an agent's place in the hierarchy, as a spawn asks for it.
type Role struct {
	// Leader gives the agent a squad of its own.
	Leader bool
	// LeadBy is the leader it reports to ("" = the admiral).
	LeadBy string
}

// SquadTeam is the team name a leader's squad is mirrored into: the
// leader's own name, which is already a legal team name.
func SquadTeam(leader string) string { return leader }

// SquadCaps are the effective [squad] caps, re-read from config.toml so a
// change takes effect without restarting fleet (like [memory]).
func (f *Fleet) SquadCaps() prefs.Squad {
	if p, err := prefs.Load(); err == nil {
		return p.Squad()
	}
	return f.Prefs.Squad()
}

// checkRole validates a spawn's Role against the board and the caps.
func (f *Fleet) checkRole(name string, r Role) error {
	if r.LeadBy == "" && !r.Leader {
		return nil
	}
	caps := f.SquadCaps()
	b := f.Store.Load()
	if r.LeadBy != "" {
		up, ok := b.Agents[r.LeadBy]
		if !ok {
			return fmt.Errorf("no agent %q to lead %q", r.LeadBy, name)
		}
		if !up.Leader {
			return fmt.Errorf("agent %q is not a leader", r.LeadBy)
		}
	}
	// Depth first: "your agents may not lead squads of their own" is the
	// more useful answer when both caps would refuse the spawn.
	if r.Leader {
		if d := depthOf(b, r.LeadBy) + 1; caps.MaxDepth > 0 && d > caps.MaxDepth {
			return fmt.Errorf("max_depth %d: %s is already at depth %d, so its agents cannot lead a squad of their own",
				caps.MaxDepth, r.LeadBy, d-1)
		}
	}
	if r.LeadBy != "" {
		if n, limit := len(squadOf(b, r.LeadBy)), caps.MaxMembers; limit > 0 && n >= limit {
			return fmt.Errorf("squad %s is full: %d of max_members %d; close a member first", r.LeadBy, n, limit)
		}
	}
	return nil
}

// depthOf is how far below the admiral an agent sits: 0 for the admiral
// itself (name ""), 1 for an agent it spawned, and so on. A cycle on disk
// stops the walk rather than hanging.
func depthOf(b *board.Board, name string) int {
	d := 0
	seen := map[string]bool{}
	for name != "" && !seen[name] {
		seen[name] = true
		a, ok := b.Agents[name]
		if !ok {
			break
		}
		d++
		name = a.LeadBy
	}
	return d
}

// squadOf returns a leader's members, sorted by name.
func squadOf(b *board.Board, leader string) []*board.Agent {
	var out []*board.Agent
	for _, a := range b.Agents {
		if a.LeadBy == leader {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Squad returns a leader's members, sorted by name.
func (f *Fleet) Squad(leader string) []*board.Agent { return squadOf(f.Store.Load(), leader) }

// IsLeader reports whether name leads a squad.
func (f *Fleet) IsLeader(name string) bool {
	a, ok := f.Store.Load().Agents[name]
	return ok && a.Leader
}

// ReportsTo is the mailbox an agent's reports go to: its leader, or the
// admiral when it has none (ADR-53). A leader that has gone from the board
// hands its members back to the admiral rather than losing their reports.
func (f *Fleet) ReportsTo(name string) string {
	b := f.Store.Load()
	a, ok := b.Agents[name]
	if !ok || a.LeadBy == "" {
		return ""
	}
	if up, ok := b.Agents[a.LeadBy]; !ok || !up.Leader {
		return ""
	}
	return a.LeadBy
}

// Leaders lists the live leaders on the board, sorted by name. The wake
// loop uses it: every leader has a mailbox of its own to deliver.
func (f *Fleet) Leaders() []string {
	var out []string
	for _, a := range f.Store.Load().Sorted() {
		if a.Leader {
			out = append(out, a.Name)
		}
	}
	return out
}

// SquadMember resolves a name a leader acted on. It is the scope check for
// every squad tool: a leader may only touch its own members.
func (f *Fleet) SquadMember(leader, name string) (*board.Agent, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	b := f.Store.Load()
	a, ok := b.Agents[name]
	if !ok {
		return nil, fmt.Errorf("no agent %q", name)
	}
	if a.LeadBy != leader {
		return nil, fmt.Errorf("%s is not in your squad; you can only act on the agents you spawned (%s)",
			name, squadNames(squadOf(b, leader)))
	}
	return a, nil
}

func squadNames(as []*board.Agent) string {
	if len(as) == 0 {
		return "none yet"
	}
	var ns []string
	for _, a := range as {
		ns = append(ns, a.Name)
	}
	return strings.Join(ns, ", ")
}

// MakeLeader gives a leader its squad team, so the squad shows on the board
// in one colour and leader and members may message each other freely.
func (f *Fleet) MakeLeader(name string) {
	if f.Teams == nil {
		return
	}
	if err := f.Teams.AddSquad(SquadTeam(name), name, name); err != nil {
		logx.For("fleet").Warn("squad team not created", "leader", name, "err", err)
	}
}

// SquadSpawn is a leader spawning one of its own agents: the same profiles,
// worktrees, task cards and playbook as an admiral spawn, with the squad
// caps and max_working applied, and the member joined to the squad team.
func (f *Fleet) SquadSpawn(leader string, o SpawnOpts, name, profile, dir, prompt string) (*board.Agent, error) {
	if !f.IsLeader(leader) {
		return nil, fmt.Errorf("agent %q does not lead a squad", leader)
	}
	o.Role = Role{LeadBy: leader, Leader: false}
	a, err := f.SpawnWith(o, name, profile, dir, prompt)
	if err != nil {
		return nil, err
	}
	if f.Teams != nil {
		if err := f.Teams.AddSquad(SquadTeam(leader), leader, name); err != nil {
			logx.For("fleet").Warn("squad team not joined", "leader", leader, "agent", name, "err", err)
		}
	}
	logx.For("fleet").Info("squad spawn", "leader", leader, "agent", name, "profile", profile, "dir", dir)
	return a, nil
}

// SquadNote is what a close or a status line says about a leader's live
// squad ("" when it has none).
func SquadNote(leader string, members []*board.Agent) string {
	live := LiveSquad(members)
	if len(live) == 0 {
		return ""
	}
	return fmt.Sprintf("%s still leads %d agent(s): %s. They keep running and report to the admiral instead; close them first to stop them too",
		leader, len(live), squadNames(live))
}

// LiveSquad is the members that still have work in flight: everything but
// an agent that is already gone from tmux is counted, because the board is
// what the admiral acts on.
func LiveSquad(members []*board.Agent) []*board.Agent {
	var out []*board.Agent
	for _, a := range members {
		if a.Status != string(status.Parked) {
			out = append(out, a)
		}
	}
	return out
}

// disband detaches a closing leader's members (they report to the admiral
// from then on) and removes the squad team once it is empty.
func (f *Fleet) disband(name string) {
	b := f.Store.Load()
	a, ok := b.Agents[name]
	if !ok || !a.Leader {
		return
	}
	members := squadOf(b, name)
	if err := f.Store.Update(func(b *board.Board) error {
		for _, m := range members {
			if cur, ok := b.Agents[m.Name]; ok && cur.LeadBy == name {
				cur.LeadBy = ""
			}
		}
		return nil
	}); err != nil {
		logx.For("fleet").Warn("squad not detached", "leader", name, "err", err)
	}
	if len(members) > 0 {
		logx.For("fleet").Info("squad detached, members report to the admiral", "leader", name, "members", len(members))
	}
	// The mirror team goes with the leader: left behind it would keep the
	// ex-members one colour and messaging each other freely forever, and an
	// agent later spawned with the leader's name would join them (ADR-53).
	// Only the team fleet made for this squad goes; a user's team that
	// happens to share the name stays (ADR-59).
	if f.Teams != nil {
		if err := f.Teams.DeleteSquad(SquadTeam(name), name); err != nil {
			logx.For("fleet").Warn("squad team not deleted", "leader", name, "err", err)
		}
	}
	f.clearBox(name)
}

// clearBox retires a closed leader's mailbox: its unread messages are
// marked read, the squad's reports among them re-filed to the admiral, and
// the wake queue is removed, so nothing is left to type into the pane of
// whatever agent is next given this name (ADR-53). The drain returns
// exactly what it marked read, so no report is lost in between (ADR-59).
func (f *Fleet) clearBox(name string) {
	if ms := f.Mail(); ms != nil {
		drained, err := ms.Inbox(name, false, 0)
		if err != nil {
			logx.For("fleet").Warn("leader mailbox not drained", "leader", name, "err", err)
		}
		for _, m := range drained {
			if m.Kind == mail.KindReport {
				f.refile(name, m)
			}
		}
	}
	for _, p := range []string{f.wakePath(name), f.triesPath(name), f.wakePath(name) + ".lock"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			logx.For("fleet").Warn("leader wake queue not removed", "leader", name, "path", p, "err", err)
		}
	}
}

// refile hands one unread squad report from a closed leader's mailbox to
// the admiral, and wakes it for done, blocked and question like any report.
func (f *Fleet) refile(leader string, m mail.Msg) {
	log := logx.For("fleet")
	st, sum, _ := strings.Cut(m.Text, ": ")
	sum += " (re-filed: its leader " + leader + " was closed before reading it)"
	nm, err := f.Mail().ReportTo(m.From, mail.Admiral, st, sum)
	if err != nil {
		log.Error("squad report lost with its closed leader", "leader", leader, "agent", m.From, "state", st, "err", err)
		return
	}
	log.Info("squad report re-filed to the admiral", "leader", leader, "agent", m.From, "state", st)
	if st == "done" || st == "blocked" || st == "question" {
		f.wakeSupervisor(nm.ID, m.From, st, ReportLineTo(mail.Supervisor, m.From, st, mail.FoldSummary(sum)))
	}
}

// SquadCloseAll closes a leader's members, newest first, and returns one
// result per member.
func (f *Fleet) SquadCloseAll(leader string) []Result {
	var rs []Result
	for _, m := range f.Squad(leader) {
		r := Result{Agent: m.Name, OK: true}
		if err := f.Close(m.Name); err != nil {
			r.OK, r.Note = false, err.Error()
		}
		rs = append(rs, r)
	}
	return rs
}

// SameSquad reports whether a and b are a leader and one of its members,
// or two members of one squad: they may message each other without a grant
// whatever the agent_mail policy says (ADR-53).
func (f *Fleet) SameSquad(a, b string) bool {
	ag := f.Store.Load().Agents
	x, okA := ag[a]
	y, okB := ag[b]
	if !okA || !okB || a == b {
		return false
	}
	switch {
	case x.LeadBy == b && y.Leader, y.LeadBy == a && x.Leader:
		return true
	case x.LeadBy != "" && x.LeadBy == y.LeadBy:
		return true
	}
	return false
}

// boxOf turns a leader name into a mailbox name; "" is the admiral's.
func boxOf(leader string) string {
	if leader == "" {
		return mail.Supervisor
	}
	return leader
}
