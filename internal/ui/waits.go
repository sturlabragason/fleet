package ui

import (
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/waits"
)

// WaitsBackend is what the w view and the r answer view need (ADR-167).
// Optional, so a backend without it says so instead.
type WaitsBackend interface {
	Waits() []waits.Item
	Pending(name string) (fleet.Pending, error)
	Reply(name string, r fleet.Reply, expect string) (string, error)
}

// WaitsActor is what the w view's actions need (ADR-168): d marks a todo
// done, r/p/l resume, pause or let go a thread. Optional.
type WaitsActor interface {
	DoneWait(it waits.Item) (string, error)
	ThreadAction(id, action, reason string) error
}

// waitsView is the w view that replaces the tiles while open: a tree of
// section, context and parent (ADR-168), or the flat "waits on you" list
// (ADR-167) after T.
type waitsView struct {
	all    []waits.Item // everything Waits returned, oldest first
	loaded bool
	rows   []wrow
	sel    int
	off    int
	flat   bool            // T: the flat list of section Waiting
	byCtx  bool            // G: context, then section
	ctx    string          // the context chip shown; "" is all
	filter string          // /: rows whose text holds it
	folded map[string]bool // a group row's fold, where the user changed it
	marked map[string]bool // x: thread ids to let go with L
	input  *wInput         // typing a filter or a let-go reason
	click  wclick          // the last row clicked, for a double click
}

// wInput is a line being typed: the filter, or the reason for letting the
// threads in refs go.
type wInput struct {
	letgo bool
	refs  []string
	text  string
}

type wclick struct {
	row int
	at  time.Time
}

type waitsMsg []waits.Item

// waitsActedMsg is an action's outcome and the list read after it.
type waitsActedMsg struct {
	info  string
	items []waits.Item
}

// openWaits is w: open the view and load it.
func (m *Model) openWaits() tea.Cmd {
	if _, ok := m.be.(WaitsBackend); !ok {
		m.msg = "w needs fleet's backend"
		return nil
	}
	m.wv = &waitsView{folded: map[string]bool{}, marked: map[string]bool{}}
	return m.loadWaits()
}

// loadWaits reads the list off the UI loop; each poll calls it while the
// view is open, so it stays as fresh as the tiles.
func (m *Model) loadWaits() tea.Cmd {
	wb, ok := m.be.(WaitsBackend)
	if !ok {
		return nil
	}
	return func() tea.Msg { return waitsMsg(wb.Waits()) }
}

// set takes a new list and keeps the cursor on the same row. Marks of
// threads no longer listed (let go) are dropped; a failed let-go keeps its.
func (v *waitsView) set(items []waits.Item) {
	v.all, v.loaded = items, true
	listed := map[string]bool{}
	for _, it := range items {
		listed[it.Ref] = true
	}
	for id := range v.marked {
		if !listed[id] {
			delete(v.marked, id)
		}
	}
	v.rebuild()
}

// cur is the selected row, if any.
func (v *waitsView) cur() (wrow, bool) {
	if v.sel < 0 || v.sel >= len(v.rows) {
		return wrow{}, false
	}
	return v.rows[v.sel], true
}

// waitsKeys are the w view's keys; a footer label click runs the same.
var waitsKeys = map[string]func(m *Model) tea.Cmd{
	"esc":    (*Model).waitsEsc,
	"q":      (*Model).closeWaits,
	"w":      (*Model).closeWaits,
	"ctrl+c": func(*Model) tea.Cmd { return tea.Quit },
	"up":     func(m *Model) tea.Cmd { m.wv.move(-1); return nil },
	"k":      func(m *Model) tea.Cmd { m.wv.move(-1); return nil },
	"down":   func(m *Model) tea.Cmd { m.wv.move(1); return nil },
	"j":      func(m *Model) tea.Cmd { m.wv.move(1); return nil },
	"pgup":   func(m *Model) tea.Cmd { m.wv.move(-max(m.h/2, 1)); return nil },
	"pgdown": func(m *Model) tea.Cmd { m.wv.move(max(m.h/2, 1)); return nil },
	"home":   func(m *Model) tea.Cmd { m.wv.sel = 0; return nil },
	"g":      func(m *Model) tea.Cmd { m.wv.sel = 0; return nil },
	"end":    func(m *Model) tea.Cmd { m.wv.sel = max(len(m.wv.rows)-1, 0); return nil },
	"left":   func(m *Model) tea.Cmd { m.wv.fold(true); return nil },
	"right":  func(m *Model) tea.Cmd { m.wv.fold(false); return nil },
	"enter":  (*Model).waitsEnter,
	" ":      (*Model).waitsPeek,
	"space":  (*Model).waitsPeek,
	"r":      (*Model).waitsAnswer,
	"d":      (*Model).waitsDone,
	"p":      func(m *Model) tea.Cmd { return m.threadAct(fleet.ThreadPause, "") },
	"l":      (*Model).waitsLetGo,
	"x":      (*Model).waitsMark,
	"L":      (*Model).waitsLetGoMarked,
	"/":      func(m *Model) tea.Cmd { m.wv.input = &wInput{text: m.wv.filter}; return nil },
	"T":      func(m *Model) tea.Cmd { m.wv.flat = !m.wv.flat; m.wv.rebuild(); return nil },
	"G":      func(m *Model) tea.Cmd { m.wv.byCtx = !m.wv.byCtx; m.wv.rebuild(); return nil },
	"c":      func(m *Model) tea.Cmd { m.wv.nextCtx(); return nil },
	"1":      func(m *Model) tea.Cmd { m.wv.jump(waits.Waiting); return nil },
	"2":      func(m *Model) tea.Cmd { m.wv.jump(waits.Running); return nil },
	"3":      func(m *Model) tea.Cmd { m.wv.jump(waits.Open); return nil },
	"4":      func(m *Model) tea.Cmd { m.wv.jump(waits.Quiet); return nil },
}

func (m *Model) waitsKey(k string) tea.Cmd {
	if m.wv.input != nil {
		return m.waitsInputKey(k)
	}
	if h := waitsKeys[k]; h != nil {
		return h(m)
	}
	return nil
}

func (m *Model) closeWaits() tea.Cmd { m.wv = nil; return nil }

// waitsEsc clears the filter first, then closes.
func (m *Model) waitsEsc() tea.Cmd {
	if m.wv.filter != "" {
		m.wv.filter = ""
		m.wv.rebuild()
		return nil
	}
	return m.closeWaits()
}

func (v *waitsView) move(d int) {
	v.sel = min(max(v.sel+d, 0), max(len(v.rows)-1, 0))
}

// waitsInputKey edits the line being typed: the filter applies as it is
// typed, a reason lets the threads go on ⏎.
func (m *Model) waitsInputKey(k string) tea.Cmd {
	v, in := m.wv, m.wv.input
	switch k {
	case "esc":
		v.input = nil
		if !in.letgo {
			v.filter = ""
			v.rebuild()
		}
		return nil
	case "enter":
		v.input = nil
		if in.letgo {
			return m.letGo(in.refs, in.text)
		}
		return nil
	case "backspace":
		r := []rune(in.text)
		in.text = string(r[:max(len(r)-1, 0)])
	case "space":
		in.text += " "
	default:
		if r := []rune(k); len(r) != 1 || len([]rune(in.text)) >= 200 {
			return nil
		}
		in.text += k
	}
	if !in.letgo {
		v.filter = in.text
		v.rebuild()
	}
	return nil
}

// item is the selected row's item; ok false on a group row.
func (m *Model) item() (waits.Item, bool) {
	r, ok := m.wv.cur()
	if !ok || r.item == nil {
		return waits.Item{}, false
	}
	return *r.item, true
}

// boardAgent selects the row's agent on the overview, or says why not.
func (m *Model) boardAgent(it waits.Item) bool {
	if it.Agent == "" || m.agent(it.Agent) == nil {
		m.msg, m.msgAt = it.Source+" is not an agent on the board", m.now()
		return false
	}
	m.sel = it.Agent
	m.saveCursor()
	return true
}

// waitsEnter folds a group row, or puts the row's agent in ▶.
func (m *Model) waitsEnter() tea.Cmd {
	r, ok := m.wv.cur()
	if !ok {
		return nil
	}
	if r.item == nil {
		m.wv.toggle(r)
		return nil
	}
	if !m.boardAgent(*r.item) {
		return nil
	}
	m.wv = nil
	m.reveal()
	return m.show()
}

func (m *Model) waitsPeek() tea.Cmd {
	if it, ok := m.item(); ok && m.boardAgent(it) {
		return m.preview()
	}
	return nil
}

// waitsAnswer is r: resume a thread, else answer the row's agent.
func (m *Model) waitsAnswer() tea.Cmd {
	it, ok := m.item()
	if !ok {
		return nil
	}
	if it.Kind == waits.Thread {
		return m.threadAct(fleet.ThreadResume, "")
	}
	if !m.boardAgent(it) {
		return nil
	}
	return m.openAnswer(it.Agent)
}

// waitsDone is d: complete the row's todo item.
func (m *Model) waitsDone() tea.Cmd {
	it, ok := m.item()
	if !ok || !it.Todo() {
		m.msg, m.msgAt = "d marks a todo item done; this row is not one", m.now()
		return nil
	}
	return m.waitsAct(func(a WaitsActor) (string, error) {
		line, err := a.DoneWait(it)
		return "done " + line, err
	})
}

// thread is the selected row's thread id, or a message saying it is none.
func (m *Model) thread() (string, bool) {
	it, ok := m.item()
	if !ok || it.Kind != waits.Thread || it.Ref == "" {
		m.msg, m.msgAt = "p, l, x and r resume act on thread rows", m.now()
		return "", false
	}
	return it.Ref, true
}

func (m *Model) threadAct(action, reason string) tea.Cmd {
	id, ok := m.thread()
	if !ok {
		return nil
	}
	return m.waitsAct(func(a WaitsActor) (string, error) {
		return action + " " + id, a.ThreadAction(id, action, reason)
	})
}

func (m *Model) waitsLetGo() tea.Cmd {
	if id, ok := m.thread(); ok {
		m.wv.input = &wInput{letgo: true, refs: []string{id}}
	}
	return nil
}

func (m *Model) waitsMark() tea.Cmd {
	if id, ok := m.thread(); ok {
		if m.wv.marked[id] {
			delete(m.wv.marked, id)
		} else {
			m.wv.marked[id] = true
		}
		m.wv.move(1)
	}
	return nil
}

// waitsLetGoMarked is L: ask one reason for every marked thread still listed.
func (m *Model) waitsLetGoMarked() tea.Cmd {
	var refs []string
	for _, it := range m.wv.all {
		if it.Kind == waits.Thread && m.wv.marked[it.Ref] && !slices.Contains(refs, it.Ref) {
			refs = append(refs, it.Ref)
		}
	}
	if len(refs) == 0 {
		m.msg, m.msgAt = "x marks thread rows; nothing is marked", m.now()
		return nil
	}
	m.wv.input = &wInput{letgo: true, refs: refs}
	return nil
}

func (m *Model) letGo(refs []string, reason string) tea.Cmd {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "let go from the overview"
	}
	return m.waitsAct(func(a WaitsActor) (string, error) {
		for _, id := range refs {
			if err := a.ThreadAction(id, fleet.ThreadLetGo, reason); err != nil {
				return "", err
			}
		}
		return "let go " + strings.Join(refs, " "), nil
	})
}

// waitsAct runs an action off the UI loop and reloads the list after it.
func (m *Model) waitsAct(do func(a WaitsActor) (string, error)) tea.Cmd {
	a, ok := m.be.(WaitsActor)
	wb, _ := m.be.(WaitsBackend)
	if !ok || wb == nil {
		m.msg, m.msgAt = "this action needs fleet's backend", m.now()
		return nil
	}
	return func() tea.Msg {
		info, err := do(a)
		if err != nil {
			info = err.Error()
		}
		return waitsActedMsg{info: info, items: wb.Waits()}
	}
}

func (m *Model) agent(name string) *board.Agent {
	for _, a := range m.agents {
		if a.Name == name {
			return a
		}
	}
	return nil
}
