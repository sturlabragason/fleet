// Package mail is the inter-agent mailbox and access grants (DESIGN §6.6,
// ADR-19), stored under ~/.fleet/mail with one flock and atomic rewrites.
// It knows nothing about tmux or the board; callers check that agents exist.
package mail

import (
	"encoding/json"
	"fmt"
	"github.com/sturlabragason/fleet/internal/jsonx"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sturlabragason/fleet/internal/legacy"
	"github.com/sturlabragason/fleet/internal/logx"
)

// User is the decider name for the human user.
const User = "user"

// MaxText is the message size cap in bytes.
const MaxText = 8 << 10

// Pair states.
const (
	Pending = "pending"
	Granted = "granted"
	Denied  = "denied"
)

// Message kinds.
const (
	KindMsg     = "msg"
	KindRequest = "request"
	KindGrant   = "grant"
	KindDeny    = "deny"
	KindRevoke  = "revoke"
)

// Pair is the access state of one unordered pair of agents (A < B).
type Pair struct {
	A           string    `json:"a"`
	B           string    `json:"b"`
	State       string    `json:"state"`
	RequestedBy string    `json:"requested_by,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	DecidedBy   string    `json:"decided_by,omitempty"`
	Requested   time.Time `json:"requested,omitzero"`
	Decided     time.Time `json:"decided,omitzero"`

	// extra keeps keys this build does not know (ADR-62).
	extra jsonx.Extra
}

// Other returns the side of the pair that is not name.
func (p Pair) Other(name string) string {
	if p.A == name {
		return p.B
	}
	return p.A
}

// Has reports whether name is one side of the pair.
func (p Pair) Has(name string) bool { return p.A == name || p.B == name }

// Msg is one inbox line.
type Msg struct {
	ID   string    `json:"id"`
	From string    `json:"from"`
	To   string    `json:"to"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
	T    time.Time `json:"t"`
	// FYI marks a message that needs no action: it waits for the
	// recipient's next turn instead of waking it (ADR-92).
	FYI bool `json:"fyi,omitempty"`
}

// Store is the mailbox rooted at Dir (<fleet home>/mail).
type Store struct {
	Dir string
	// Implicit reports whether a and b may message each other without a
	// grant (same team, same repo, or an open policy; ADR-34). nil = never.
	// An explicit pair wins: granted allows, denied (deny or revoke) blocks.
	Implicit func(a, b string) bool
	// Peers lists the agents a broadcast from a may reach through Implicit;
	// granted pairs are always included. nil = granted pairs only.
	Peers func(a string) []string
	// Hint is added to a "no access" refusal: who else from may reach
	// freely about to (fleet#35). nil or "" = nothing.
	Hint func(from, to string) string
}

// New returns a store rooted at dir.
func New(dir string) *Store { return &Store{Dir: dir} }

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)

// checkName keeps agent names safe to use as directory names. The
// `admiral.` prefix is reserved for admirals' mailboxes (DESIGN §6.12).
func checkName(names ...string) error {
	for _, n := range names {
		if !nameRe.MatchString(n) || n == User || n == Admiral || n == LegacySupervisor || strings.HasPrefix(n, AdmiralPrefix) {
			return fmt.Errorf("bad agent name %q", n)
		}
	}
	return nil
}

// checkBox is checkName for a mailbox owner: an admiral has one too (main
// under either name; LegacySupervisor maps to the same box).
func checkBox(agent string) error {
	if IsAdmiralBox(agent) {
		return nil
	}
	return checkName(agent)
}

// checkPair validates the two sides of a mail pair: two agents, or two
// admirals. Admirals do not mail agents and agents do not mail admirals.
func checkPair(a, b string) error {
	if IsAdmiralBox(a) != IsAdmiralBox(b) && b != "*" {
		return fmt.Errorf("admirals and agents do not message each other: admirals use fleet_send, agents report")
	}
	if err := checkBox(a); err != nil {
		return err
	}
	if b == "*" {
		return nil
	}
	return checkBox(b)
}

func key(a, b string) (string, string) {
	if a > b {
		return b, a
	}
	return a, b
}

// agentDir is mail/<a>. The admiral's box is mail/admiral under either name;
// a mail/supervisor box from before ADR-26 moves there under the mail lock
// (migrateAdmiral) and stays as a symlink, so older binaries still write to
// the same inbox. Until then reads use the old dir, so nothing is missed.
func (s *Store) agentDir(a string) string {
	if a == Admiral || a == LegacySupervisor {
		return legacy.Resolve(filepath.Join(s.Dir, Admiral), filepath.Join(s.Dir, LegacySupervisor))
	}
	return filepath.Join(s.Dir, a)
}

// migrateAdmiral moves mail/supervisor to mail/admiral and links it. It runs
// only under mail.lock: an old binary's deliver creates mail/supervisor and
// appends to its inbox under the same lock, so an unlocked rename could split
// the box or lose a report.
func (s *Store) migrateAdmiral() {
	legacy.MoveDir(filepath.Join(s.Dir, Admiral), filepath.Join(s.Dir, LegacySupervisor))
}

// mkAgentDir creates agentDir(a) (and, for the admiral, the legacy link);
// it runs under the lock (deliver).
func (s *Store) mkAgentDir(a string) error {
	if a == Admiral || a == LegacySupervisor {
		_, err := legacy.MkdirAll(filepath.Join(s.Dir, Admiral), filepath.Join(s.Dir, LegacySupervisor))
		return err
	}
	return os.MkdirAll(s.agentDir(a), 0o755)
}

func (s *Store) inboxPath(a string) string { return filepath.Join(s.agentDir(a), "inbox.jsonl") }
func (s *Store) readPath(a string) string  { return filepath.Join(s.agentDir(a), "read") }
func (s *Store) msgsReadPath(a string) string {
	return filepath.Join(s.agentDir(a), "read_msgs")
}
func (s *Store) nudgePath(a string) string { return filepath.Join(s.agentDir(a), "nudged") }
func (s *Store) wokenPath(a string) string { return filepath.Join(s.agentDir(a), "woken") }
func (s *Store) grantsPath() string        { return filepath.Join(s.Dir, "grants.json") }
func (s *Store) lastPath() string          { return filepath.Join(s.Dir, "last_id") }

// state is what a mutation sees under the lock.
type state struct {
	s     *Store
	pairs []Pair
	dirty bool
	last  string // last issued message id
}

// implicit is Store.Implicit, false when unset.
func (s *Store) implicit(a, b string) bool {
	return s.Implicit != nil && a != b && s.Implicit(a, b)
}

// allowed reports whether from may message to: a granted pair, or no
// explicit decision against it and implicit access.
func (st *state) allowed(from, to string) bool {
	p := st.find(from, to)
	if p != nil && p.State == Granted {
		return true
	}
	if p != nil && p.State == Denied {
		return false
	}
	return st.s.implicit(from, to)
}

func (st *state) find(a, b string) *Pair {
	a, b = key(a, b)
	for i := range st.pairs {
		if st.pairs[i].A == a && st.pairs[i].B == b {
			return &st.pairs[i]
		}
	}
	return nil
}

// nextID returns a strictly increasing, sortable id: zero-padded unix nanos,
// bumped past the last issued one (mail/last_id survives restarts).
func (st *state) nextID() string {
	n := time.Now().UnixNano()
	if l, err := strconv.ParseInt(st.last, 10, 64); err == nil && n <= l {
		n = l + 1
	}
	st.last = fmt.Sprintf("%020d", n)
	return st.last
}

// deliver appends one message to inbox's file (O_APPEND, under the lock).
func (st *state) deliver(inbox, id, from, to, kind, text string) error {
	return st.deliverMsg(inbox, Msg{ID: id, From: from, To: to, Kind: kind, Text: text, T: time.Now()})
}

func (st *state) deliverMsg(inbox string, m Msg) error {
	if err := st.s.mkAgentDir(inbox); err != nil {
		return err
	}
	f, err := os.OpenFile(st.s.inboxPath(inbox), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, _ := json.Marshal(m)
	_, err = f.Write(append(data, '\n'))
	return err
}

// notify delivers a control message (request/grant/deny/revoke) with a fresh id.
func (st *state) notify(inbox, from, kind, text string) error {
	return st.deliver(inbox, st.nextID(), from, inbox, kind, text)
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readTrim(path string) string {
	b, _ := os.ReadFile(path)
	return strings.TrimSpace(string(b))
}

func (s *Store) loadPairs() []Pair {
	data, err := os.ReadFile(s.grantsPath())
	if err != nil {
		return nil
	}
	var ps []Pair
	if err := json.Unmarshal(data, &ps); err != nil {
		logx.For("mail").Error("broken grants.json, starting empty", "err", err)
		_ = os.WriteFile(s.grantsPath()+".broken", data, 0o644)
		return nil
	}
	return ps
}

// update runs fn under the mail.lock flock, then saves last_id and, when
// changed, grants.json (both atomically).
func (s *Store) update(fn func(*state) error) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	lf, err := os.OpenFile(filepath.Join(s.Dir, "mail.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	s.migrateAdmiral()
	st := &state{s: s, pairs: s.loadPairs(), last: readTrim(s.lastPath())}
	prev := st.last
	ferr := fn(st)
	// Lines may be appended even when fn fails later; keep ids monotonic.
	if st.last != prev {
		if err := writeAtomic(s.lastPath(), []byte(st.last)); err != nil {
			return err
		}
	}
	if ferr != nil || !st.dirty {
		return ferr
	}
	sort.Slice(st.pairs, func(i, j int) bool {
		if st.pairs[i].A != st.pairs[j].A {
			return st.pairs[i].A < st.pairs[j].A
		}
		return st.pairs[i].B < st.pairs[j].B
	})
	data, _ := json.MarshalIndent(st.pairs, "", "  ")
	return writeAtomic(s.grantsPath(), data)
}

// All returns every pair, sorted.
func (s *Store) All() []Pair { return s.loadPairs() }

// Pairs returns the pairs that include agent.
func (s *Store) Pairs(agent string) []Pair {
	var out []Pair
	for _, p := range s.loadPairs() {
		if p.Has(agent) {
			out = append(out, p)
		}
	}
	return out
}

// Allowed reports whether from may message to now (grant or implicit access).
func (s *Store) Allowed(from, to string) bool {
	st := &state{s: s, pairs: s.loadPairs()}
	return from != to && st.allowed(from, to)
}

// PairOf returns the pair of a and b, if any.
func (s *Store) PairOf(a, b string) (Pair, bool) {
	a, b = key(a, b)
	for _, p := range s.loadPairs() {
		if p.A == a && p.B == b {
			return p, true
		}
	}
	return Pair{}, false
}

// pairJSON is Pair without its methods, for jsonx.
type pairJSON Pair

func (x *Pair) UnmarshalJSON(b []byte) error {
	var v pairJSON
	e, err := jsonx.Unmarshal(b, &v)
	*x, x.extra = Pair(v), e
	return err
}

func (x Pair) MarshalJSON() ([]byte, error) { return jsonx.Marshal(pairJSON(x), x.extra) }
