package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/worktree"
)

// ErrNotAgent is returned when the caller isn't a fleet agent.
var ErrNotAgent = fmt.Errorf("not a fleet agent (run inside a fleet pane or pass --as NAME)")

// Mail returns the mailbox store under <home>/mail (DESIGN §6.6), with the
// agent_mail policy wired in as implicit access (ADR-34).
func (f *Fleet) Mail() *mail.Store {
	m := mail.New(filepath.Join(f.Home, "mail"))
	repos := map[string]string{} // dir -> git common dir, per store
	policy := f.MailPolicy()
	m.Implicit = func(a, b string) bool {
		if mail.IsAdmiralBox(a) || mail.IsAdmiralBox(b) {
			// Admirals reach each other freely only under open (§6.12).
			return a != b && mail.IsAdmiralBox(a) && mail.IsAdmiralBox(b) && policy == prefs.MailOpen
		}
		return f.mailReason(policy, a, b, repos) != ""
	}
	m.Peers = func(a string) []string {
		var out []string
		if mail.IsAdmiralBox(a) {
			for _, b := range f.AdmiralBoxes() {
				if b != mail.BoxOf(mail.AdmiralOf(a)) {
					out = append(out, b)
				}
			}
			return out
		}
		for _, ag := range f.Store.Load().Sorted() {
			if ag.Name != a {
				out = append(out, ag.Name)
			}
		}
		return out
	}
	return m
}

// MailPolicy is the effective agent_mail setting: team, open or ask. It
// rereads config.toml, so a long-running agent MCP server sees a change; a
// file that no longer parses keeps the value loaded at start.
func (f *Fleet) MailPolicy() string {
	if p, err := prefs.Load(); err == nil {
		return p.AgentMail()
	}
	return f.Prefs.AgentMail()
}

// mailReason says why a and b may message without a grant: "open" (policy),
// "team" (same team) or "repo" (same repo, neither in a team); "" = they
// must ask. Agents in different teams, or one in a team and one not, ask
// under the team policy: teams can be different clients (ADR-34).
func (f *Fleet) mailReason(policy, a, b string, repos map[string]string) string {
	ag := f.Store.Load().Agents
	x, okA := ag[a]
	y, okB := ag[b]
	if !okA || !okB || a == b {
		return ""
	}
	// A leader and its own agents, and two agents of one squad, always
	// reach each other: the hierarchy is the grant (ADR-53).
	if f.SameSquad(a, b) {
		return "squad"
	}
	switch policy {
	case prefs.MailOpen:
		return "open"
	case prefs.MailAsk:
		return ""
	}
	var ta, tb string
	if f.Teams != nil {
		ta, _ = f.Teams.Of(a)
		tb, _ = f.Teams.Of(b)
	}
	if ta != "" || tb != "" {
		if ta == tb {
			return "team"
		}
		return ""
	}
	if ra, rb := repoOf(x.Dir, repos), repoOf(y.Dir, repos); ra != "" && ra == rb {
		return "repo"
	}
	return ""
}

func repoOf(dir string, cache map[string]string) string {
	if dir == "" {
		return ""
	}
	if r, ok := cache[dir]; ok {
		return r
	}
	r, _ := worktree.CommonDir(dir)
	cache[dir] = r
	return r
}

// Whoami returns the agent the caller runs as: override when set, else the
// @fleet_agent tag of $TMUX_PANE on this fleet's tmux server ("" if none).
func (f *Fleet) Whoami(override string) string {
	if override != "" {
		return override
	}
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return ""
	}
	// $TMUX is "<socket path>,<pid>,<session>": a pane id from another tmux
	// server would name an unrelated pane here.
	if sock, _, _ := strings.Cut(os.Getenv("TMUX"), ","); sock == "" || sock != f.T.SocketPath() {
		return ""
	}
	name, err := f.T.Run("display-message", "-p", "-t", pane, "#{@fleet_agent}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(name)
}

// Identity is Whoami, but inside an agent pane an override must name that
// same agent: one agent can't act as another by passing --as/--agent.
func (f *Fleet) Identity(override string) (string, error) {
	pane := f.Whoami("")
	if override != "" && pane != "" && override != pane {
		return "", fmt.Errorf("this pane is agent %q; it can't act as %q", pane, override)
	}
	if override != "" {
		return override, nil
	}
	return pane, nil
}

// UserOnly refuses user-side decisions (fleet access grant/deny/revoke) from
// inside an agent pane, so an agent can't grant itself as "user".
func (f *Fleet) UserOnly() error {
	if me := f.Whoami(""); me != "" {
		return fmt.Errorf("fleet access is for the user (this pane is agent %q); agents use fleet mail request/grant/deny/revoke", me)
	}
	return nil
}

// Me resolves the caller and checks it is on the board.
func (f *Fleet) Me(override string) (string, error) {
	me, err := f.Identity(override)
	if err != nil {
		return "", err
	}
	if me == "" {
		return "", ErrNotAgent
	}
	return me, f.onBoard(me)
}

func (f *Fleet) onBoard(names ...string) error {
	b := f.Store.Load()
	for _, n := range names {
		if mail.IsAdmiralBox(n) { // an admiral is not on the board: it is registered
			if !supervisor.Exists(f.Home, mail.AdmiralOf(n)) {
				return fmt.Errorf("no admiral %q", mail.AdmiralOf(n))
			}
			continue
		}
		if _, ok := b.Agents[n]; !ok {
			return fmt.Errorf("no agent %q", n)
		}
	}
	return nil
}

// Peer is one row of MailAgents.
type Peer struct {
	Name, Profile, Status, Dir string
	// Access: none, granted, denied, pending (you asked), pending (asks
	// you), or free (team|repo|open) for implicit access (ADR-34).
	Access string
}

// MailAgents lists the other agents on the board with my access state.
func (f *Fleet) MailAgents(me string) []Peer {
	m := f.Mail()
	repos := map[string]string{}
	policy := f.MailPolicy()
	var out []Peer
	for _, a := range f.Store.Load().Sorted() {
		if a.Name == me {
			continue
		}
		acc := "none"
		p, ok := m.PairOf(me, a.Name)
		if ok {
			acc = p.State
			if p.State == mail.Pending {
				acc = "pending (asks you)"
				if p.RequestedBy == me {
					acc = "pending (you asked)"
				}
			}
		}
		if !ok || p.State == mail.Pending {
			if r := f.mailReason(policy, me, a.Name, repos); r != "" {
				acc = "free (" + r + ")"
			}
		}
		out = append(out, Peer{Name: a.Name, Profile: a.Profile, Status: a.Status, Dir: a.Dir, Access: acc})
	}
	return out
}

// MailRequest asks for access to another agent on the board.
func (f *Fleet) MailRequest(me, to, reason string) (string, error) {
	if err := f.onBoard(me, to); err != nil {
		return "", err
	}
	st, err := f.Mail().Request(me, to, reason)
	if err == nil {
		f.nudge(me, to)
		f.announceAdmiral(me, to)
	}
	return st, err
}

// MailSend messages one agent or "*" (every peer I may message) and nudges idle recipients.
func (f *Fleet) MailSend(me, to, text string) ([]string, error) {
	if to == "*" {
		if err := f.onBoard(me); err != nil {
			return nil, err
		}
	} else if err := f.onBoard(me, to); err != nil {
		return nil, err
	}
	rcpt, err := f.Mail().Send(me, to, text)
	if err != nil {
		return nil, err
	}
	f.nudge(me, rcpt...)
	f.announceAdmiral(me, rcpt...)
	return rcpt, nil
}

// AccessDecide is the user's grant/deny/revoke of a pair. Granting needs
// both agents on the board.
func (f *Fleet) AccessDecide(a, b, action string) error {
	if action == "grant" {
		if err := f.onBoard(a, b); err != nil {
			return err
		}
	}
	return f.Mail().Decide(a, b, action)
}

// nudge types one line into each idle recipient's pane, once per unread batch.
func (f *Fleet) nudge(from string, to ...string) {
	log := logx.For("mail")
	ag := f.Store.Load().Agents
	for _, r := range to {
		a, ok := ag[r]
		if !ok || a.Status != string(status.Idle) {
			continue
		}
		pane := f.paneOf(r)
		if pane == "" {
			continue
		}
		n, ok, err := f.Mail().ClaimNudge(r)
		if err != nil || !ok {
			continue
		}
		if err := f.paste(pane, NudgeLine(n, from)); err != nil {
			log.Warn("nudge failed", "agent", r, "err", err)
			continue
		}
		log.Info("nudged", "agent", r, "from", from, "unread", n)
	}
}

func (f *Fleet) paste(pane, text string) error {
	if f.Paste != nil {
		return f.Paste(pane, text)
	}
	return f.T.Paste(pane, text, true)
}

// NudgeLine is the line typed into an idle recipient's pane.
func NudgeLine(n int, from string) string {
	if n == 1 {
		return fmt.Sprintf("[fleet] 1 new message from %s. Read it with fleet_inbox (or: fleet mail inbox)", from)
	}
	return fmt.Sprintf("[fleet] %d new messages, the latest from %s. Read them with fleet_inbox (or: fleet mail inbox)", n, from)
}

// forgetMail revokes every grant of a closed agent; the mailbox stays.
func (f *Fleet) forgetMail(name string) {
	if err := f.Mail().ForgetAgent(name); err != nil {
		logx.For("mail").Warn("forget failed", "agent", name, "err", err)
	}
}
