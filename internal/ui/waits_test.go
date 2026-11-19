package ui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/waits"
)

// waitsBE is fakeBE with the w/r backend: bravo waits on a picker, and
// Reply refuses once the prompt on screen is no longer what the user saw.
// Its actions are recorded in acts and drop the item from the list.
type waitsBE struct {
	fakeBE
	prompt  fleet.Pending
	replies []string // "name|option|yn|text|expect"
	acts    []string // "done ref" or "action id reason"
	gone    map[string]bool
}

func (b *waitsBE) Waits() []waits.Item {
	all := []waits.Item{
		{Kind: "ask", Source: "main", Ask: "reply to the Teams thread", Since: time.Now().Add(-48 * time.Hour), Ref: "aaaa1111", Section: waits.Waiting, Parent: "main", Ctx: "work"},
		{Kind: waits.Thread, Source: "notes", Ask: "review the draft", Since: time.Now().Add(-5 * time.Hour), Ref: "th-notes", Section: waits.Quiet, Parent: "notes", Ctx: "home"},
		{Kind: waits.Thread, Source: "deck", Ask: "slides 6-9", Since: time.Now().Add(-4 * time.Hour), Ref: "th-deck", Section: waits.Quiet, Parent: "deck", Ctx: "work"},
		{Kind: waits.Working, Source: "alpha", Agent: "alpha", Ask: "build it", Since: time.Now().Add(-time.Hour), Section: waits.Running, Parent: "alpha"},
		{Kind: waits.Prompt, Source: "bravo", Agent: "bravo", Ask: "Approve? [y/n]", Since: time.Now().Add(-time.Minute), Section: waits.Waiting, Parent: "bravo", Ctx: "work"},
	}
	var out []waits.Item
	for _, it := range all {
		if !b.gone[it.Ref] {
			out = append(out, it)
		}
	}
	return out
}
func (b *waitsBE) DoneWait(it waits.Item) (string, error) {
	b.acts = append(b.acts, "done "+it.Ref)
	b.gone[it.Ref] = true
	return it.Ref, nil
}
func (b *waitsBE) ThreadAction(id, action, reason string) error {
	b.acts = append(b.acts, strings.TrimSpace(action+" "+id+" "+reason))
	b.gone[id] = true
	return nil
}
func (b *waitsBE) Pending(string) (fleet.Pending, error) { return b.prompt, nil }
func (b *waitsBE) Reply(name string, r fleet.Reply, expect string) (string, error) {
	b.replies = append(b.replies, strings.Join([]string{name, string(rune('0' + r.Option)), r.YN, r.Text, expect}, "|"))
	if err := fleet.Gate(b.prompt, expect); err != nil {
		return "", err
	}
	return "chose ok", nil
}

// run feeds a key and every message its commands return back into m.
func run(m *Model, k string) {
	var msg tea.Msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "backspace":
		msg = tea.KeyMsg{Type: tea.KeyBackspace}
	case " ":
		msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	}
	_, cmd := m.Update(msg)
	for cmd != nil {
		out := cmd()
		if out == nil {
			return
		}
		_, cmd = m.Update(out)
	}
}

func TestWaitsAnswerFlow(t *testing.T) {
	be := &waitsBE{prompt: fleet.Pending{Question: "Allow write to go.mod?", Options: []string{"1. Yes", "2. No"}}}
	zone.NewGlobal()
	m := New(be, "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	as, _ := be.Refresh()
	m.setAgents(as)

	run(m, "w")
	run(m, "T")
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Waits on you  flat · oldest 2d") || strings.Index(v, "Teams thread") > strings.Index(v, "Approve?") || strings.Contains(v, "build it") {
		t.Fatalf("flat waits view, waiting only, oldest first:\n%s", v)
	}
	run(m, "down")
	run(m, "r")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Answer bravo") || !strings.Contains(v, "Allow write to go.mod?") || !strings.Contains(v, "2. No") {
		t.Fatalf("answer view must show the exact prompt and options:\n%s", v)
	}
	run(m, "2")
	if len(be.replies) != 0 || !strings.Contains(ansi.Strip(m.View()), `Send "2. No" to bravo?`) {
		t.Fatalf("a pick must ask to confirm before sending:\n%s", ansi.Strip(m.View()))
	}

	// The prompt changes before ⏎: the send is refused and the view shows the new one.
	be.prompt = fleet.Pending{Question: "Delete the branch?", Options: []string{"1. Yes", "2. No"}}
	run(m, "enter")
	if len(be.replies) != 1 || !strings.HasSuffix(be.replies[0], "|Allow write to go.mod? | 1. Yes | 2. No") {
		t.Fatalf("reply must carry the prompt the user saw as expect: %q", be.replies)
	}
	v = ansi.Strip(m.View())
	if m.ans == nil || !strings.Contains(v, "prompt changed") || !strings.Contains(v, "Delete the branch?") {
		t.Fatalf("a changed prompt must be refused and shown:\n%s", v)
	}

	run(m, "1")
	run(m, "enter")
	if m.ans != nil || m.wv == nil || !strings.Contains(m.msg, "bravo: chose ok") {
		t.Fatalf("a confirmed answer closes back to the list: ans=%v msg=%q", m.ans, m.msg)
	}
	run(m, "esc")
	if m.wv != nil {
		t.Fatal("esc closes the list")
	}
}

// TestWaitsTree: the tree groups section, context and parent, folds
// Running and Quiet, and its keys act on the right rows.
func TestWaitsTree(t *testing.T) {
	be := &waitsBE{gone: map[string]bool{}}
	zone.NewGlobal()
	m := New(be, "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	as, _ := be.Refresh()
	m.setAgents(as)
	run(m, "w")
	v := ansi.Strip(m.View())
	for _, want := range []string{"▾ WAITING ON YOU  2", "▾ work  2", "◆ bravo", "Approve?", "● r answer", "d done", "▸ RUNNING  1   ⠋ alpha", "▸ QUIET  2", "■ 2 waiting on you", "[all 5]"} {
		if !strings.Contains(v, want) {
			t.Fatalf("tree misses %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "build it") || strings.Contains(v, "slides 6-9") {
		t.Fatalf("Running and Quiet start folded:\n%s", v)
	}

	// d on the ask completes it and the list reloads.
	m.wv.sel = rowOf(t, m, "reply to the Teams thread")
	run(m, "d")
	if len(be.acts) != 1 || be.acts[0] != "done aaaa1111" || strings.Contains(ansi.Strip(m.View()), "Teams thread") {
		t.Fatalf("d: acts %q\n%s", be.acts, ansi.Strip(m.View()))
	}

	// 4 opens Quiet; x marks both threads and L lets them go with one reason.
	run(m, "4")
	if !strings.Contains(ansi.Strip(m.View()), "slides 6-9") {
		t.Fatalf("4 jumps to Quiet and unfolds it:\n%s", ansi.Strip(m.View()))
	}
	for _, ask := range []string{"review the draft", "slides 6-9"} {
		m.wv.sel = rowOf(t, m, ask)
		run(m, "x")
	}
	for _, k := range []string{"L", "o", "l", "d", "enter"} {
		run(m, k)
	}
	if len(be.acts) != 3 || !strings.Contains(strings.Join(be.acts, ","), "letgo th-notes old") || !strings.Contains(strings.Join(be.acts, ","), "letgo th-deck old") {
		t.Fatalf("L: acts %q", be.acts)
	}

	// p on a row that is no thread says so and does nothing.
	m.wv.sel = rowOf(t, m, "Approve? [y/n]")
	run(m, "p")
	if len(be.acts) != 3 || !strings.Contains(m.msg, "thread rows") {
		t.Fatalf("p on a prompt row: acts %q msg %q", be.acts, m.msg)
	}

	// A filter keeps matching rows; G puts context first.
	for _, k := range []string{"/", "a", "p", "p", "r", "enter"} {
		run(m, k)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Approve?") || strings.Contains(v, "RUNNING") || !strings.Contains(v, "/appr") {
		t.Fatalf("filter:\n%s", v)
	}
	run(m, "esc")
	run(m, "G")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "by context") || strings.Index(v, "work") > strings.Index(v, "WAITING ON YOU") {
		t.Fatalf("G groups by context first:\n%s", v)
	}
}

// rowOf is the index of the item row whose ask is ask.
func rowOf(t *testing.T, m *Model, ask string) int {
	t.Helper()
	for i, r := range m.wv.rows {
		if r.item != nil && r.item.Ask == ask {
			return i
		}
	}
	t.Fatalf("no row %q", ask)
	return 0
}

func TestAnswerTypedReply(t *testing.T) {
	be := &waitsBE{prompt: fleet.Pending{Question: "Teal or blue?", Report: "question"}}
	zone.NewGlobal()
	m := New(be, "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	as, _ := be.Refresh()
	m.setAgents(as)
	m.sel = "echo"
	run(m, "r")
	run(m, "1") // no options: a number is not a pick
	if m.ans.confirm {
		t.Fatal("a number picked an option that is not there")
	}
	for _, k := range []string{"t", "t", "e", "a", "l", "enter", "enter"} {
		run(m, k)
	}
	if len(be.replies) != 1 || be.replies[0] != "echo|0||teal|Teal or blue?" {
		t.Fatalf("typed reply = %q", be.replies)
	}
}

func TestAnswerNeedsBackend(t *testing.T) {
	m := New(&fakeBE{}, "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	run(m, "w")
	run(m, "r")
	if m.wv != nil || m.ans != nil {
		t.Fatal("w and r opened without a backend for them")
	}
}

// TestWaitsClicks: a click on a section unfolds it, a chip filters by
// context and a footer label is its key.
func TestWaitsClicks(t *testing.T) {
	be := &waitsBE{gone: map[string]bool{}}
	zone.NewGlobal()
	m := New(be, "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	run(m, "w")
	click := func(id string) {
		t.Helper()
		m.View()
		deadline := time.Now().Add(2 * time.Second)
		for zone.Get(id).IsZero() { // zones register asynchronously
			if time.Now().After(deadline) {
				t.Fatalf("no zone %s", id)
			}
			time.Sleep(time.Millisecond)
		}
		z := zone.Get(id)
		m.Update(tea.MouseMsg{X: z.StartX, Y: z.StartY, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	}
	running := -1
	for i, r := range m.wv.rows {
		if r.level == lvlSection && r.name == waits.Running {
			running = i
		}
	}
	click("wrow" + strconv.Itoa(running))
	if v := ansi.Strip(m.View()); !strings.Contains(v, "build it") || m.wv.sel != running {
		t.Fatalf("a click on RUNNING selects and unfolds it:\n%s", v)
	}
	click("wchip:home")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "[home 1]") || strings.Contains(v, "Approve?") {
		t.Fatalf("the home chip shows only home:\n%s", v)
	}
	click("wkey:T")
	if !m.wv.flat {
		t.Fatal("the T label is a button")
	}
}
