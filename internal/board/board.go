// Package board stores agent state in ~/.fleet/board.json (flock + atomic rename)
// and appends status transitions to events.jsonl.
package board

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/sturlabragason/fleet/internal/jsonx"
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
	// Closing is when a close of an acp agent began: its host exits cleanly
	// on the stop request before fleet kills the window, and the dead-pane
	// reaper must not report that as a death (ADR-74).
	Closing time.Time `json:"closing,omitzero"`
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
	// Admiral is the admiral that owns the agent (DESIGN §6.12, ADR-64).
	// "" is main, which is what every agent had before several admirals.
	Admiral string `json:"admiral,omitempty"`
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
	// PromptAt is when fleet last handed the agent work: its spawn task or a
	// text fleet_send. A turn that ends after it with no final report is
	// "no report" (ADR-68).
	PromptAt time.Time `json:"prompt_at,omitempty"`
	// NoReportTold is when the owner was told this agent stopped without a
	// report; once per task, so a newer PromptAt re-arms it (ADR-68).
	NoReportTold time.Time `json:"no_report_told,omitempty"`
	// RecycleTold is when the owner was told to hand this agent's work to a
	// fresh agent; once per agent (ADR-71).
	RecycleTold time.Time `json:"recycle_told,omitempty"`
	// TurnAt is when the agent's latest turn began: the refresher's last
	// switch to working after an idle of at least NoReportAfter (a shorter
	// idle is a boundary inside one turn). It is what the user's agents are
	// judged by, since what the user types sets no PromptAt (ADR-68).
	TurnAt time.Time `json:"turn_at,omitempty"`
	// Driver is "acp" for an agent run under `fleet acp-host` (DESIGN §7d,
	// ADR-74); "" is the tmux driver, which every agent had before it.
	Driver string `json:"driver,omitempty"`
	// ACPServer names the ACP server an acp agent's host was started with
	// (tools.Profile.ACPLabel, ADR-77): "copilot --acp", "agy acp",
	// "claude-agent-acp@0.103.0". "" on a tmux agent or one started before
	// v1.2.0.
	ACPServer string `json:"acp_server,omitempty"`
	// FleetVersion is the fleet build that last started the agent's window
	// (spawn, wake or adopt; ADR-77). "" before v1.2.0.
	FleetVersion string `json:"fleet_version,omitempty"`

	// extra holds board keys this build does not know, so they survive a
	// round trip (ADR-58).
	extra jsonx.Extra
}

// DriverACP is Agent.Driver for an agent run over ACP.
const DriverACP = "acp"

// ACP reports whether the agent runs under the acp driver.
func (a *Agent) ACP() bool { return a != nil && a.Driver == DriverACP }

// DriverName is "acp" or "tmux"; an agent with no driver recorded (every
// one before v1.0.0) runs on tmux.
func (a *Agent) DriverName() string {
	if a.ACP() {
		return DriverACP
	}
	return "tmux"
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
	// May lists standing permissions the user gave for this task, such as
	// merge-local, push-main or deploy (free text, fleet#32).
	May []string `json:"may,omitempty"`
	// Checked is the last done-check run; nil until one has finished.
	Checked *Check `json:"checked,omitempty"`
	// Started is when a done-check run was claimed. It is the claim marker
	// that keeps one check to one run (ADR-43): a second done report, or a
	// sweep, sees it and stands down. A claim older than the timeout is
	// stale — its runner died — and may be taken again.
	Started time.Time `json:"started,omitempty"`
	extra   jsonx.Extra
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
	Out   string `json:"out,omitempty"`
	extra jsonx.Extra
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
	extra  jsonx.Extra
}

// Exit is how an agent's process ended, as tmux reported it.
type Exit struct {
	// Code is the exit status; nil when killed by a signal or tmux did not say.
	Code *int `json:"code,omitempty"`
	// Signal is the signal number that ended it, 0 for none.
	Signal int       `json:"signal,omitempty"`
	At     time.Time `json:"at"`
	extra  jsonx.Extra
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

// NoReportAfter is how long an agent must sit idle before a missing report
// counts: a turn boundary inside one task (autopilot, a background shell
// finishing) is idle for a moment and then works again.
var NoReportAfter = 20 * time.Second

// NoReport reports whether the agent ended its turn mid-task: idle for
// NoReportAfter, given work by fleet, and with no done, blocked or question
// report since then. A progress report is not an ending. An agent the user
// is talking to is never flagged: idle there is waiting for the user (ADR-68).
// A screen that changed within NoReportAfter is not idle, whatever the
// patterns read (fleet#12). Once the owner was told in this idle spell the
// flag stays, so a ticking footer (agy's quota countdown) can't blink it.
func (a *Agent) NoReport(now time.Time) bool {
	if a.Status != "idle" || a.PromptAt.IsZero() || a.WithUser(now) {
		return false
	}
	told := !a.NoReportTold.Before(a.PromptAt) && !a.NoReportTold.Before(a.StatusSince)
	if !told && !a.stillFor(now, NoReportAfter) {
		return false
	}
	r := a.Report
	return r == nil || r.At.Before(a.PromptAt) || r.State == "progress"
}

// stillFor reports whether the agent has been idle and its pane screen
// unchanged for at least d.
func (a *Agent) stillFor(now time.Time, d time.Duration) bool {
	return now.Sub(a.StatusSince) >= d && now.Sub(a.LastChange) >= d
}

// UserTyping is how recent the user's last key press in an agent's pane
// must be to count as typing there right now (ADR-61, ADR-68).
var UserTyping = 30 * time.Second

// UserNoReport is NoReport for an agent the user is talking to: its latest
// turn ended NoReportAfter ago with no report of any kind since the turn
// began, and the user has not pressed a key in its pane for UserTyping.
// Such an agent reports progress, not done, so any report ends a turn. The
// board marks it softly and nobody is woken (ADR-68 amendment).
func (a *Agent) UserNoReport(now time.Time) bool {
	if a.Status != "idle" || a.TurnAt.IsZero() || !a.WithUser(now) || !a.stillFor(now, NoReportAfter) {
		return false
	}
	if !a.UserAt.IsZero() && now.Sub(a.UserAt) < UserTyping {
		return false
	}
	return a.Report == nil || a.Report.At.Before(a.TurnAt)
}

// NoReportTag is the agent's @fleet_noreport pane value: "1" for NoReport,
// "user" for UserNoReport, "" for neither.
func (a *Agent) NoReportTag(now time.Time) string {
	switch {
	case a.NoReport(now):
		return "1"
	case a.UserNoReport(now):
		return "user"
	}
	return ""
}

// Main is the name of the admiral every fleet has (DESIGN §6.12).
const Main = "main"

// Owner is the name of the admiral that owns the agent: main when none is set.
func (a *Agent) Owner() string {
	if a.Admiral == "" {
		return Main
	}
	return a.Admiral
}

// SetOwner gives the agent to an admiral; main is stored as "" so the board
// stays what older builds wrote.
func (a *Agent) SetOwner(name string) {
	if name == Main {
		name = ""
	}
	a.Admiral = name
}

// GitBase is a work tree's state before an agent started.
type GitBase struct {
	Head      string            `json:"head"`
	Dirty     map[string]string `json:"dirty,omitempty"` // path -> "XY fingerprint"
	Worktrees []string          `json:"worktrees,omitempty"`
	extra     jsonx.Extra
}

// Report is one agent report: done, blocked, question or progress.
type Report struct {
	State   string    `json:"state"`
	Summary string    `json:"summary"`
	At      time.Time `json:"at"`
	extra   jsonx.Extra
}

// Board is the whole file.
type Board struct {
	Agents map[string]*Agent `json:"agents"`
	extra  jsonx.Extra
}

// Store reads and writes the board under a directory.
type Store struct{ Dir string }

// NewStore returns a store rooted at dir.
func NewStore(dir string) *Store { return &Store{Dir: dir} }

func (s *Store) path() string { return filepath.Join(s.Dir, "board.json") }

// Load reads the board. Broken JSON is logged, backed up and treated as empty.
func (s *Store) Load() *Board {
	b, _ := s.load()
	return b
}

// load is Load plus the bytes it read, so Update can tell a no-op.
func (s *Store) load() (*Board, []byte) {
	b := &Board{Agents: map[string]*Agent{}}
	data, err := os.ReadFile(s.path())
	if err != nil {
		return b, nil
	}
	if err := json.Unmarshal(data, b); err != nil {
		logx.For("board").Error("broken board.json, starting empty", "err", err)
		_ = os.WriteFile(s.path()+".broken", data, 0o644)
		return &Board{Agents: map[string]*Agent{}}, nil
	}
	if b.Agents == nil {
		b.Agents = map[string]*Agent{}
	}
	return b, data
}

// Update runs fn on the board under an exclusive lock and saves the result.
// A result identical to the file on disk is not written: the watcher
// refreshes every second, and an idle fleet would otherwise rewrite the
// board each time (ADR-62).
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
	b, old := s.load()
	if err := fn(b); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(b, "", "  ")
	if old != nil && bytes.Equal(data, old) {
		return nil
	}
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
