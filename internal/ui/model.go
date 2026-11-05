package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/layout"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/team"
	"github.com/sturlabragason/fleet/internal/wall"
)

// Model is the overview state.
type Model struct {
	be       Backend
	agents   []*board.Agent // attention-sorted
	queuePos map[string]int // queued agent -> 1-based start position
	onWall   map[string]bool
	teams    map[string]team.Info
	sel      string
	w, h     int
	frame    int
	animate  bool
	msg      string
	msgAt    time.Time // when msg was set; it clears after msgTTL
	target   string    // agent in the ▶ target slot, "" if none (DESIGN.md §5e)
	help     bool      // ? overlay with every hotkey replaces the tiles
	helpOff  int       // first overlay line shown when it is taller than the pane
	stateDir string
	cfg      layout.Config
	now      func() time.Time
	supCtx   [2]string // last label/colour set on the admiral border
	ctxSet   bool
}

type agentsMsg struct {
	agents []*board.Agent
	onWall map[string]bool
	teams  map[string]team.Info
	target string
	supCtx [2]string
	err    error
}

// wallMsg is the outcome of a wall action: a message for the header and,
// on success, the agent now in the ▶ slot (also selected).
type wallMsg struct{ info, target string }

// msgTTL is how long a header message replaces the hint line.
const msgTTL = 5 * time.Second

type pollMsg struct{}
type animMsg struct{}
type infoMsg string

// New builds a model. stateDir holds ui.json (cursor survives restarts).
func New(be Backend, stateDir string) *Model {
	m := &Model{be: be, onWall: map[string]bool{}, stateDir: stateDir, cfg: layout.Default, now: time.Now}
	if b, err := os.ReadFile(filepath.Join(stateDir, "ui.json")); err == nil {
		var s struct{ Selected string }
		if json.Unmarshal(b, &s) == nil {
			m.sel = s.Selected
		}
	}
	return m
}

func (m *Model) saveCursor() {
	if m.stateDir == "" {
		return
	}
	b, _ := json.Marshal(struct{ Selected string }{m.sel})
	os.WriteFile(filepath.Join(m.stateDir, "ui.json"), b, 0o644)
}

func (m *Model) poll() tea.Cmd {
	be := m.be
	return func() tea.Msg {
		as, err := be.Refresh()
		// Re-tile on every poll: nothing else notices a status flip, so this
		// is what makes emphasis move when a pane starts waiting (§5b.3).
		_ = be.ApplyWall()
		l, c := be.SupCtx()
		return agentsMsg{agents: as, onWall: be.OnWall(), teams: be.Teams(), target: be.Target(), supCtx: [2]string{l, c}, err: err}
	}
}

// Init starts polling.
func (m *Model) Init() tea.Cmd { return tea.Batch(m.poll(), anim()) }

func anim() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return animMsg{} })
}

func pollLater() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return pollMsg{} })
}

// Update handles messages; panics are logged and swallowed.
func (m *Model) Update(msg tea.Msg) (_ tea.Model, cmd tea.Cmd) {
	prevMsg := m.msg
	defer func() { // runs after the recover below, so it sees its message too
		if m.msg != prevMsg {
			m.msgAt = m.now()
		}
	}()
	defer func() {
		if r := recover(); r != nil {
			logx.For("ui").Error("panic in Update", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			m.msg = fmt.Sprintf("internal error: %v (logged)", r)
			cmd = nil
		}
	}()
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case agentsMsg:
		if msg.err != nil {
			m.msg = msg.err.Error()
		}
		m.setAgents(msg.agents)
		m.onWall = msg.onWall
		m.teams = msg.teams
		m.target = msg.target
		return m, tea.Batch(pollLater(), m.syncSupCtx(msg.supCtx))
	case pollMsg:
		return m, m.poll()
	case animMsg:
		m.frame++
		if m.msg != "" && m.now().Sub(m.msgAt) >= msgTTL {
			m.msg = ""
		}
		return m, anim()
	case infoMsg:
		m.msg, m.msgAt = string(msg), m.now()
	case wallMsg:
		m.msg, m.msgAt = msg.info, m.now()
		if msg.target != "" {
			m.target, m.sel = msg.target, msg.target
			m.saveCursor()
		}
	case tea.KeyMsg:
		return m, m.key(msg.String())
	case tea.MouseMsg:
		if m.help && msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.scrollHelp(-1)
			case tea.MouseButtonWheelDown:
				m.scrollHelp(1)
			}
			return m, nil
		}
		if !m.help && msg.Action == tea.MouseActionRelease && msg.Button == tea.MouseButtonLeft {
			for _, a := range m.agents {
				if zone.Get("tile-" + a.Name).InBounds(msg) {
					m.sel = a.Name
					m.saveCursor()
					return m, m.show()
				}
			}
		}
	}
	return m, nil
}

func (m *Model) setAgents(as []*board.Agent) {
	sort.SliceStable(as, func(i, j int) bool {
		ri, rj := rank(as[i].Status), rank(as[j].Status)
		if ri != rj {
			return ri < rj
		}
		if as[i].Status == "waiting" && !as[i].StatusSince.Equal(as[j].StatusSince) {
			return as[i].StatusSince.Before(as[j].StatusSince) // longest wait first
		}
		return as[i].Name < as[j].Name
	})
	m.agents = as
	m.queuePos = map[string]int{}
	for i, a := range fleet.QueueOrder(as) {
		m.queuePos[a.Name] = i + 1
	}
	if m.index() < 0 && len(as) > 0 {
		m.sel = as[0].Name
	}
}

func (m *Model) index() int {
	for i, a := range m.agents {
		if a.Name == m.sel {
			return i
		}
	}
	return -1
}

func (m *Model) cols() int {
	return m.grid().Cols
}

func (m *Model) key(k string) tea.Cmd {
	i := m.index()
	n := len(m.agents)
	move := func(d int) {
		if n == 0 {
			return
		}
		j := i + d
		if j < 0 || j >= n {
			return
		}
		m.sel = m.agents[j].Name
		m.saveCursor()
	}
	if m.help {
		switch k {
		case "q", "esc", "?": // q closes the overlay, it doesn't quit
			m.help, m.helpOff = false, 0
		case "up", "k":
			m.scrollHelp(-1)
		case "down", "j":
			m.scrollHelp(1)
		case "pgup":
			m.scrollHelp(-max(m.h-2, 1))
		case "pgdown":
			m.scrollHelp(max(m.h-2, 1))
		case "ctrl+c":
			return tea.Quit
		}
		return nil
	}
	switch k {
	case "q", "ctrl+c":
		return tea.Quit
	case "?":
		m.help = true
	case "left", "h":
		move(-1)
	case "right", "l":
		move(1)
	case "up", "k":
		move(-m.cols())
	case "down", "j":
		move(m.cols())
	case "tab", "shift+tab":
		dir := 1
		if k == "shift+tab" {
			dir = -1
		}
		if m.be.WallEnabled() {
			return m.next(dir)
		}
		// No wall: just move the cursor through the waiting tiles, which
		// sort first, longest wait first.
		var waiting []string
		for _, a := range m.agents {
			if a.Status == "waiting" {
				waiting = append(waiting, a.Name)
			}
		}
		if len(waiting) == 0 {
			m.msg = wall.NothingWaitingMsg
			return nil
		}
		j := 0
		for x, nm := range waiting {
			if nm == m.sel {
				j = ((x+dir)%len(waiting) + len(waiting)) % len(waiting)
			}
		}
		m.sel = waiting[j]
		m.saveCursor()
	case "enter":
		return m.show()
	case "a", "+":
		return m.add()
	case "esc":
		if m.target == "" {
			return nil
		}
		m.target, m.msg = "", "▶ cleared"
		be := m.be
		return func() tea.Msg {
			if err := be.Untarget(); err != nil {
				logx.For("ui").Error("untarget", "err", err)
				return infoMsg("clear ▶: " + err.Error())
			}
			return nil
		}
	case "R":
		if !m.be.WallEnabled() {
			m.msg = "R needs fleet up"
			return nil
		}
		be := m.be
		return func() tea.Msg {
			if err := be.ResetSup(); err != nil {
				return infoMsg("reset: " + err.Error())
			}
			return nil
		}
	case "P":
		if !m.be.WallEnabled() {
			m.msg = "P needs fleet up"
			return nil
		}
		be := m.be
		return func() tea.Msg {
			if err := be.PickProfile(); err != nil {
				logx.For("ui").Error("profile picker", "err", err)
				return infoMsg("profile: " + err.Error())
			}
			return nil
		}
	case "g":
		if !m.be.WallEnabled() {
			m.msg = "g needs fleet up"
			return nil
		}
		be := m.be
		return func() tea.Msg {
			if err := be.Changes(); err != nil {
				logx.For("ui").Error("changes popup", "err", err)
				return infoMsg("changes: " + err.Error())
			}
			return nil
		}
	case "x":
		name, be := m.sel, m.be
		if name == "" || !m.onWall[name] {
			m.msg = "x: " + name + " is not on the wall"
			return nil
		}
		return func() tea.Msg {
			if err := be.Hide(name); err != nil {
				logx.For("ui").Error("hide failed", "agent", name, "err", err)
				return infoMsg("hide " + name + ": " + err.Error())
			}
			return infoMsg(name + " off wall")
		}
	case "1", "2", "4", "6", "9", "m":
		be := m.be
		return func() tea.Msg {
			if err := be.Preset(k); err != nil {
				return infoMsg(err.Error())
			}
			return infoMsg("wall layout " + k)
		}
	}
	return nil
}

// wallReady reports whether the live wall is there, else says so.
func (m *Model) wallReady() bool {
	if !m.be.WallEnabled() {
		m.msg = "live wall needs `fleet up` (TUI must run inside the fleet tmux server)"
		return false
	}
	return true
}

// show puts the selected agent into the ▶ slot, or onto the wall when there
// is no target (the wall decides, DESIGN.md §5e).
func (m *Model) show() tea.Cmd {
	name, be := m.sel, m.be
	if name == "" || !m.wallReady() {
		return nil
	}
	return func() tea.Msg {
		before, wasOn := be.Target(), be.OnWall()[name]
		ok, err := be.Show(name)
		if err != nil {
			logx.For("ui").Error("show failed", "agent", name, "err", err)
			return infoMsg("show " + name + ": " + err.Error())
		}
		if !ok {
			return infoMsg(wall.FullMsg)
		}
		return shownMsg(be, name, before, wasOn)
	}
}

// shownMsg says what a Show or Next did, from the ▶ agent before it and
// whether the agent was already on the wall: it replaced the ▶ pane, was
// appended, or was already there.
func shownMsg(be Backend, name, before string, wasOn bool) wallMsg {
	switch {
	case wasOn:
		return wallMsg{name + " on wall", name}
	case before != "" && before != name && !be.OnWall()[before]:
		return wallMsg{name + " → ▶ (was " + before + ")", name}
	}
	return wallMsg{name + " added to wall", name}
}

// add puts the selected agent onto the wall as a new pane.
func (m *Model) add() tea.Cmd {
	name, be := m.sel, m.be
	if name == "" || !m.wallReady() {
		return nil
	}
	return func() tea.Msg {
		wasOn := be.OnWall()[name]
		ok, err := be.Add(name)
		if err != nil {
			logx.For("ui").Error("add failed", "agent", name, "err", err)
			return infoMsg("add " + name + ": " + err.Error())
		}
		if !ok {
			return infoMsg(wall.AddFullMsg)
		}
		if wasOn {
			return wallMsg{name + " on wall", name}
		}
		return wallMsg{name + " added to wall", name}
	}
}

// next puts the next (dir=+1) or previous (-1) waiting agent into the ▶ slot
// and moves the cursor to it.
func (m *Model) next(dir int) tea.Cmd {
	be := m.be
	return func() tea.Msg {
		before, on := be.Target(), be.OnWall()
		name, ok, err := be.Next(dir)
		if err != nil {
			logx.For("ui").Error("next waiting failed", "dir", dir, "err", err)
			return infoMsg("next waiting: " + err.Error())
		}
		if name == "" {
			return infoMsg(wall.NothingWaitingMsg)
		}
		if !ok {
			return infoMsg(wall.FullMsg) // cursor and ▶ stay put
		}
		return shownMsg(be, name, before, on[name])
	}
}

// syncSupCtx updates the admiral border meter only when it changed.
func (m *Model) syncSupCtx(v [2]string) tea.Cmd {
	if m.ctxSet && v == m.supCtx {
		return nil
	}
	m.supCtx, m.ctxSet = v, true
	be := m.be
	return func() tea.Msg {
		if err := be.SetSupCtx(v[0], v[1]); err != nil {
			logx.For("ui").Warn("set admiral ctx", "err", err)
		}
		return nil
	}
}
