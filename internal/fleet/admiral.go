package fleet

import (
	"fmt"
	"github.com/sturlabragason/fleet/internal/theme"
	"sort"
	"strings"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/supervisor"
)

// Several admirals on one fleet (DESIGN §6.12, ADR-64). The board's
// Agent.Admiral says who owns an agent ("" = main); an admiral's mailbox is
// mail/admiral (main) or mail/admiral.<name>.

// BadgeByOption holds the per-admiral split of the unread-report badge; it
// is set only while more than one admiral exists.
const BadgeByOption = "@fleet_reports_by"

// AdmiralNames is main followed by the registered admirals.
func (f *Fleet) AdmiralNames() []string { return supervisor.Names(f.Home) }

// AdmiralBoxes is the mailbox of every admiral, main first.
func (f *Fleet) AdmiralBoxes() []string {
	var out []string
	for _, n := range f.AdmiralNames() {
		out = append(out, mail.BoxOf(n))
	}
	return out
}

// OwnerOf is the admiral that owns an agent: "main" when the agent has no
// owner, is unknown, or its admiral has been closed.
func (f *Fleet) OwnerOf(agent string) string {
	a, ok := f.Store.Load().Agents[agent]
	if !ok {
		return board.Main
	}
	if o := a.Owner(); supervisor.Exists(f.Home, o) {
		return o
	}
	return board.Main
}

// AdmiralBoxOf is the mailbox an agent's reports go to when it has no leader.
func (f *Fleet) AdmiralBoxOf(agent string) string { return mail.BoxOf(f.OwnerOf(agent)) }

// ReportBox is where an agent's report is filed: its leader's box, else its
// owner's admiral box.
func (f *Fleet) ReportBox(name string) string {
	if up := f.ReportsTo(name); up != "" {
		return up
	}
	return f.AdmiralBoxOf(name)
}

// UnreadBy counts unread reports per admiral name.
func (f *Fleet) UnreadBy() map[string]int {
	out := map[string]int{}
	for _, n := range f.AdmiralNames() {
		c := 0
		for _, m := range f.Mail().Peek(mail.BoxOf(n)) {
			if m.Kind == mail.KindReport {
				c++
			}
		}
		out[n] = c
	}
	return out
}

// BadgeBy is the split badge " ● main 1 · print 2 ": admirals with nothing
// unread are left out, and "" is returned for one admiral or no unread.
func (f *Fleet) BadgeBy() string {
	names := f.AdmiralNames()
	if len(names) < 2 {
		return ""
	}
	by := f.UnreadBy()
	var parts []string
	for _, n := range names {
		if by[n] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", n, by[n]))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " ● " + strings.Join(parts, " · ") + " "
}

// ErrOtherAdmiral is the refusal for a write on another admiral's agent.
func ErrOtherAdmiral(agent, owner string) error {
	return fmt.Errorf("%s belongs to admiral %s; ask the user to hand it over", agent, owner)
}

// CheckScope refuses admiral me acting on an agent another admiral owns.
// me "" or "main" is main.
func (f *Fleet) CheckScope(me, agent string) error {
	if _, ok := f.Store.Load().Agents[agent]; !ok {
		return nil // the caller's own "no agent" error is clearer
	}
	own, mine := f.OwnerOf(agent), me
	if mine == "" {
		mine = board.Main
	}
	if own != mine {
		return ErrOtherAdmiral(agent, own)
	}
	return nil
}

// Give hands an agent (and, for a leader, its squad) to another admiral,
// moving their unread reports with them. It returns how many reports moved.
// from "" skips the owner check (the user's hand-over).
func (f *Fleet) Give(from, agent, to string) (int, error) {
	if !supervisor.Exists(f.Home, to) {
		return 0, fmt.Errorf("no admiral %q (fleet admiral ls)", to)
	}
	b := f.Store.Load()
	a, ok := b.Agents[agent]
	if !ok {
		return 0, fmt.Errorf("no agent %q", agent)
	}
	if a.LeadBy != "" {
		return 0, fmt.Errorf("%s is in %s's squad: hand over the leader", agent, a.LeadBy)
	}
	if from != "" && f.OwnerOf(agent) != from {
		return 0, ErrOtherAdmiral(agent, f.OwnerOf(agent))
	}
	old := f.OwnerOf(agent)
	if old == to || (old == board.Main && to == "") {
		return 0, fmt.Errorf("%s already belongs to %s", agent, to)
	}
	names := []string{agent}
	for _, m := range squadOf(b, agent) {
		names = append(names, m.Name)
	}
	if err := f.Store.Update(func(bd *board.Board) error {
		for _, n := range names {
			if x, ok := bd.Agents[n]; ok {
				x.SetOwner(to)
			}
		}
		return nil
	}); err != nil {
		return 0, err
	}
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	n := f.moveReports(mail.BoxOf(old), mail.BoxOf(to), func(s string) bool { return set[s] }, fmt.Sprintf("%s handed you %s", old, agent))
	logx.For("fleet").Info("agent handed over", "agent", agent, "from", old, "to", to, "reports", n)
	return n, nil
}

// moveReports re-files unread reports from one admiral box to another and
// wakes the receiver once.
func (f *Fleet) moveReports(from, to string, who func(string) bool, why string) int {
	moved, err := f.Mail().MoveReports(from, to, who)
	if err != nil {
		logx.For("fleet").Error("reports not moved", "from", from, "to", to, "err", err)
		return 0
	}
	if len(moved) == 0 {
		return 0
	}
	last := moved[len(moved)-1]
	state, _, _ := strings.Cut(last.Text, ": ")
	f.wakeBox(to, last.ID, last.From, state, fmt.Sprintf("[fleet] %s (%d unread). Full text: call fleet_reports.", why, len(moved)))
	return len(moved)
}

// CloseAdmiral returns a closed admiral's agents and unread reports to main
// and drops it from the registry. The caller closes its pane.
func (f *Fleet) CloseAdmiral(name string) (agents, reports int, err error) {
	if name == "" || name == board.Main {
		return 0, 0, fmt.Errorf("main cannot be closed")
	}
	if !supervisor.Exists(f.Home, name) {
		return 0, 0, fmt.Errorf("no admiral %q", name)
	}
	err = f.Store.Update(func(bd *board.Board) error {
		for _, a := range bd.Agents {
			if a.Owner() == name {
				a.SetOwner(board.Main)
				agents++
			}
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	reports = f.moveReports(mail.BoxOf(name), mail.Admiral, nil, fmt.Sprintf("admiral %s closed and handed back its agents", name))
	return agents, reports, supervisor.Remove(f.Home, name)
}

// AdmiralAgents lists an admiral's agents, sorted by name.
func (f *Fleet) AdmiralAgents(name string) []string {
	var out []string
	for _, a := range f.Store.Load().Agents {
		if f.OwnerOf(a.Name) == name {
			out = append(out, a.Name)
		}
	}
	sort.Strings(out)
	return out
}

// unreadFor is what an inbox's wake queue is pruned against: an admiral
// wakes for reports and for mail, anyone else for what Peek holds.
func (f *Fleet) unreadFor(box string) []mail.Msg {
	if mail.IsAdmiralBox(box) {
		return f.Mail().PeekAll(box)
	}
	return f.Mail().Peek(box)
}

// admiralView reads one admiral's pane: the @fleet_admiral-tagged sup pane.
func (f *Fleet) admiralView(name string) AdmiralView {
	if name == board.Main && f.SupPane != nil {
		return f.SupPane()
	}
	if name != board.Main && f.AdmiralPane != nil {
		return f.AdmiralPane(name)
	}
	pane, err := supervisor.FindPaneOf(f.T, Session+":main", name)
	if err != nil {
		return AdmiralView{}
	}
	prof := supervisor.CurrentOf(f.Home, f.T, Session+":main", name).Profile
	if prof == "" {
		prof = "copilot"
	}
	set := prof
	if p, ok := f.Profiles[prof]; ok && p.Patterns != "" {
		set = p.Patterns
	}
	text, err := f.T.Capture(pane, 0)
	if err != nil {
		return AdmiralView{Pane: pane}
	}
	ps := f.Patterns[set]
	st, ok := ps.Detect(text)
	in, box := ps.InputBox(text)
	return AdmiralView{Pane: pane, Idle: ok && st == status.Idle, Box: box, Input: in}
}

// AdmiralMsgLine is the wake line for mail from another admiral.
func AdmiralMsgLine(from string) string {
	return fmt.Sprintf("[fleet] message from admiral %s. Read it with fleet_inbox.", from)
}

// announceAdmiral queues a wake for a message or access mail that landed in
// an admiral's box. It uses the guarded wake path, never a bare paste.
func (f *Fleet) announceAdmiral(fromBox string, to ...string) {
	for _, box := range to {
		if !mail.IsAdmiralBox(box) {
			continue
		}
		ms := f.Mail().PeekAll(box)
		if len(ms) == 0 {
			continue
		}
		last := ms[len(ms)-1]
		f.wakeBox(box, last.ID, mail.AdmiralOf(fromBox), "message", AdmiralMsgLine(mail.AdmiralOf(fromBox)))
	}
}

// AdmiralPeers lists the other admirals with me's access state (box names
// as Name's mailbox): free under agent_mail = open, else granted, pending
// or none (DESIGN §6.12).
func (f *Fleet) AdmiralPeers(me string) []Peer {
	m := f.Mail()
	mine := mail.BoxOf(me)
	var out []Peer
	for _, n := range f.AdmiralNames() {
		box := mail.BoxOf(n)
		if box == mine {
			continue
		}
		acc := "none"
		p, ok := m.PairOf(mine, box)
		if ok {
			acc = p.State
			if p.State == mail.Pending {
				acc = "pending (asks you)"
				if p.RequestedBy == mine {
					acc = "pending (you asked)"
				}
			}
		}
		if (!ok || p.State == mail.Pending) && m.Implicit(mine, box) {
			acc = "free (open)"
		}
		prof := supervisor.LoadChoiceOf(f.Home, n)
		if a, ok := supervisor.Find(f.Home, n); ok && a.Profile != "" {
			prof = a.Profile
		}
		out = append(out, Peer{Name: n, Profile: prof, Access: acc})
	}
	return out
}

// MailRespond is an admiral granting or denying another admiral's pending
// request, with the other side woken to read the answer.
func (f *Fleet) MailRespond(me, from string, grant bool) error {
	if err := f.Mail().Respond(me, from, grant); err != nil {
		return err
	}
	f.announceAdmiral(me, from)
	return nil
}

// MailRevoke ends the access between two admirals and wakes the other side.
func (f *Fleet) MailRevoke(me, other string) error {
	if err := f.Mail().Revoke(me, other); err != nil {
		return err
	}
	f.announceAdmiral(me, other)
	return nil
}

// MultiOption is the global tmux option that is "1" while the fleet has more
// than one admiral: borders and the badge only name admirals then.
const MultiOption = "@fleet_multi"

// syncAdmiralPanes tags every admiral pane with its name, colour and unread
// report count for the pane border (DESIGN §6.12). Only changes reach tmux.
func (f *Fleet) syncAdmiralPanes() {
	panes, err := f.T.ListPanes(Session + ":main")
	if err != nil {
		return
	}
	by := f.UnreadBy()
	for _, p := range panes {
		if p.Role != "sup" {
			continue
		}
		name := p.Admiral
		if name == "" {
			name = board.Main
		}
		want := map[string]string{
			"@fleet_admiral_name":   name,
			"@fleet_admiral_color":  theme.AdmiralColor(supervisor.Slot(f.Home, name)),
			"@fleet_admiral_unread": "",
		}
		if by[name] > 0 {
			want["@fleet_admiral_unread"] = fmt.Sprint(by[name])
		}
		for k, v := range want {
			if f.T.PaneOption(p.ID, k) == v {
				continue
			}
			if v == "" {
				f.T.UnsetPaneOption(p.ID, k)
			} else {
				f.T.SetPaneOption(p.ID, k, v)
			}
		}
	}
}
