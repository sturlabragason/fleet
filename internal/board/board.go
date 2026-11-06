// Package board stores agent state in ~/.fleet/board.json (flock + atomic rename)
// and appends status transitions to events.jsonl.
package board

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/status"
)

// Agent is one board entry.
type Agent struct {
	Name        string    `json:"name"`
	Profile     string    `json:"profile"`
	Patterns    string    `json:"patterns"`
	Dir         string    `json:"dir"`
	Goal        string    `json:"goal,omitempty"`
	SessionID   string    `json:"session_id,omitempty"`
	PaneID      string    `json:"pane_id"`
	Status      string    `json:"status"`
	StatusSince time.Time `json:"status_since"`
	LastLine    string    `json:"last_line"`
	Hash        uint64    `json:"hash"`
	LastChange  time.Time `json:"last_change"`
	Created     time.Time `json:"created"`
	// Subagents come from the tool's session event log (DESIGN.md §4
	// Subagents); SubLogOffset is how far that log has been read.
	Subagents    []status.Subagent `json:"subagents,omitempty"`
	SubLogOffset int64             `json:"sub_log_offset,omitempty"`
	// SubKnown maps recent subagent agentIds to their launch, so a
	// write_agent can re-wake one already pruned from Subagents.
	SubKnown []status.KnownSub `json:"sub_known,omitempty"`
	// Report is the agent's latest report to the admiral (DESIGN §6.7).
	Report *Report `json:"report,omitempty"`
	// Stuck is the current stuck episode and the error-repeat memory the
	// stuck rule keeps between refreshes (DESIGN §4, ADR-45).
	Stuck *status.StuckState `json:"stuck,omitempty"`
	// Baseline is the dir's git state at spawn, so auto-close can tell the
	// agent's own changes from earlier ones (DESIGN §6.7). nil: not a git
	// work tree, adopted, or spawned by an older fleet.
	Baseline *GitBase `json:"baseline,omitempty"`
	// Exit is the first sighting of the agent's dead pane (DESIGN §6.7,
	// ADR-33); set once, so each death is reported once.
	Exit *Exit `json:"exit,omitempty"`
	// Worktree is the git worktree the agent works in: the one spawn made
	// for it, or the linked worktree an adopted session was already in
	// (DESIGN §4, ADR-39). nil: it works in a plain checkout. Dir is the
	// same path; this records the branch and where it came from, so close
	// and sweep can name the work left behind.
	Worktree *Worktree `json:"worktree,omitempty"`
	// NoWorktree keeps a queued agent's opt-out, so the drain starts it the
	// way the spawn asked (DESIGN §4).
	NoWorktree bool `json:"no_worktree,omitempty"`
	// Card is the task card the spawn attached (DESIGN §18 #5/#6, ADR-43).
	// nil: the spawn had no card and the agent behaves as it always did.
	Card *Card `json:"card,omitempty"`
	// Leader marks an agent that runs a squad of its own: it gets the squad
	// peer tools and its members' reports (DESIGN §6.11, ADR-53).
	Leader bool `json:"leader,omitempty"`
	// LeadBy is the leader this agent reports to. "" is the admiral, which
	// is where every agent reported before ADR-53.
	LeadBy string `json:"lead_by,omitempty"`
	// Effort is the worker playbook level: light, standard or pro (DESIGN
	// §4, ADR-57). A queued agent keeps what the spawn asked for, "" being
	// the default; once started it holds the level actually used, which
	// wake reuses for the context args (ADR-60). "" on a started agent
	// means adopted, or started by an older fleet.
	Effort string `json:"effort,omitempty"`
	// Interactive marks an agent the user talks to directly: it is never
	// auto-closed, and fleet_send refuses to type while the user has its
	// pane open (ADR-61).
	Interactive bool `json:"interactive,omitempty"`
	// UserAt is the user's last key press seen in the agent's pane (tmux
	// client_activity while the pane was the client's active one). For
	// UserHold after it the agent counts as the user's (ADR-61).
	UserAt time.Time `json:"user_at,omitempty"`

	// extra holds board keys this build does not know, so they survive a
	// round trip (ADR-58).
	extra map[string]json.RawMessage
}

// agentJSON is Agent without its methods, so the marshallers below can use
// the ordinary struct encoding without recursing.
type agentJSON Agent

// knownAgentKeys are the json names of Agent's own fields.
var knownAgentKeys = func() map[string]bool {
	m := map[string]bool{}
	t := reflect.TypeOf(Agent{})
	for i := 0; i < t.NumField(); i++ {
		tag, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if tag != "" && tag != "-" {
			m[tag] = true
		}
	}
	return m
}()

// UnmarshalJSON keeps the keys this build has no field for. Several
// processes of different vintages write board.json — a resident watcher, the
// overview, every MCP server — and before ADR-58 the oldest of them silently
// erased whatever a newer one had written, which is how a leader lost its
// `leader` flag seconds after being spawned.
func (a *Agent) UnmarshalJSON(b []byte) error {
	var base agentJSON
	if err := json.Unmarshal(b, &base); err != nil {
		return err
	}
	*a = Agent(base)
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return err
	}
	for k := range all {
		if knownAgentKeys[k] {
			delete(all, k)
		}
	}
	if len(all) > 0 {
		a.extra = all
	}
	return nil
}

// MarshalJSON writes the known fields plus any unknown keys read back in.
func (a Agent) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal(agentJSON(a))
	if err != nil || len(a.extra) == 0 {
		return b, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return b, nil
	}
	for k, v := range a.extra {
		if _, dup := all[k]; dup || knownAgentKeys[k] {
			continue
		}
		all[k] = v
	}
	return json.Marshal(all)
}

// Card is a spawn's task card: what the task is, how fleet checks it is
// done, and where the agent writes its result (DESIGN §18 #5/#6, ADR-43).
// Every field is optional; a card with none of them is no card at all.
type Card struct {
	// Goal is the one-line task statement shown on the tile and in
	// fleet_info. Agent.Goal keeps the full first prompt.
	Goal string `json:"goal,omitempty"`
	// Check is the done-check: a shell command fleet runs in the agent's
	// dir when the agent reports done. "" means fleet cannot verify done.
	Check string `json:"check,omitempty"`
	// Want is the exit code Check must return to pass (normally 0).
	Want int `json:"want,omitempty"`
	// Result is the file the agent is told to write, relative to its dir.
	Result string `json:"result,omitempty"`
	// Checked is the last done-check run; nil until one has finished.
	Checked *Check `json:"checked,omitempty"`
	// Started is when a done-check run was claimed. It is the claim marker
	// that keeps one check to one run (ADR-43): a second done report, or a
	// sweep, sees it and stands down. A claim older than the timeout is
	// stale — its runner died — and may be taken again.
	Started time.Time `json:"started,omitempty"`
}

// Check is one done-check run.
type Check struct {
	At   time.Time `json:"at"`
	Cmd  string    `json:"cmd"`
	Want int       `json:"want"`
	// Code is the exit status, or -1 when the command never finished.
	Code int  `json:"code"`
	Pass bool `json:"pass"`
	// Err says why the command could not run or did not finish ("" = it did).
	Err string `json:"err,omitempty"`
	// Out is the tail of the combined output.
	Out string `json:"out,omitempty"`
}

// Verdict is the one-line pass/fail an admiral, a tile or fleet info shows.
func (c *Check) Verdict() string {
	switch {
	case c == nil:
		return ""
	case c.Pass:
		return "pass (exit " + strconv.Itoa(c.Code) + ")"
	case c.Err != "":
		return "fail (" + c.Err + ")"
	}
	return "fail (exit " + strconv.Itoa(c.Code) + ", want " + strconv.Itoa(c.Want) + ")"
}

// Worktree is an agent's own git worktree and branch.
type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Repo   string `json:"repo,omitempty"` // main worktree it was made from
}

// Exit is how an agent's process ended, as tmux reported it.
type Exit struct {
	// Code is the exit status; nil when killed by a signal or tmux did not say.
	Code *int `json:"code,omitempty"`
	// Signal is the signal number that ended it, 0 for none.
	Signal int       `json:"signal,omitempty"`
	At     time.Time `json:"at"`
}

// Clean reports an exit with status 0 and no signal.
func (e *Exit) Clean() bool { return e != nil && e.Code != nil && *e.Code == 0 && e.Signal == 0 }

// UserHold is how long after the user's last key press in an agent's pane
// the agent still counts as theirs: no auto-close in that time (ADR-61).
var UserHold = 10 * time.Minute

// WithUser reports whether the agent is the user's right now: flagged
// interactive, or typed in within UserHold (ADR-61).
func (a *Agent) WithUser(now time.Time) bool {
	return a.Interactive || (!a.UserAt.IsZero() && now.Sub(a.UserAt) < UserHold)
}

// GitBase is a work tree's state before an agent started.
type GitBase struct {
	Head      string            `json:"head"`
	Dirty     map[string]string `json:"dirty,omitempty"` // path -> "XY fingerprint"
	Worktrees []string          `json:"worktrees,omitempty"`
}

// Report is one agent report: done, blocked, question or progress.
type Report struct {
	State   string    `json:"state"`
	Summary string    `json:"summary"`
	At      time.Time `json:"at"`
}

// Board is the whole file.
type Board struct {
	Agents map[string]*Agent `json:"agents"`
}

// Store reads and writes the board under a directory.
type Store struct{ Dir string }

// NewStore returns a store rooted at dir.
func NewStore(dir string) *Store { return &Store{Dir: dir} }

func (s *Store) path() string { return filepath.Join(s.Dir, "board.json") }

// Load reads the board. Broken JSON is logged, backed up and treated as empty.
func (s *Store) Load() *Board {
	b := &Board{Agents: map[string]*Agent{}}
	data, err := os.ReadFile(s.path())
	if err != nil {
		return b
	}
	if err := json.Unmarshal(data, b); err != nil {
		logx.For("board").Error("broken board.json, starting empty", "err", err)
		_ = os.WriteFile(s.path()+".broken", data, 0o644)
		return &Board{Agents: map[string]*Agent{}}
	}
	if b.Agents == nil {
		b.Agents = map[string]*Agent{}
	}
	return b
}

// Update runs fn on the board under an exclusive lock and saves the result.
func (s *Store) Update(fn func(*Board) error) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	lf, err := os.OpenFile(filepath.Join(s.Dir, "board.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	b := s.Load()
	if err := fn(b); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(b, "", "  ")
	tmp := s.path() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path())
}

// Sorted returns agents sorted by name.
func (b *Board) Sorted() []*Agent {
	var out []*Agent
	for _, a := range b.Agents {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Event is one line of events.jsonl.
type Event struct {
	Time  time.Time `json:"t"`
	Agent string    `json:"agent"`
	From  string    `json:"from"`
	To    string    `json:"to"`
}

// AppendEvent appends a transition to events.jsonl.
func (s *Store) AppendEvent(e Event) {
	f, err := os.OpenFile(filepath.Join(s.Dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	data, _ := json.Marshal(e)
	f.Write(append(data, '\n'))
}
