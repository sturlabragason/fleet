package ui

import (
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
type waitsBE struct {
	fakeBE
	prompt  fleet.Pending
	replies []string // "name|option|yn|text|expect"
}

func (b *waitsBE) Waits() []waits.Item {
	return []waits.Item{
		{Kind: "ask", Source: "main", Ask: "reply to the Teams thread", Since: time.Now().Add(-48 * time.Hour)},
		{Kind: waits.Prompt, Source: "bravo", Agent: "bravo", Ask: "Approve? [y/n]", Since: time.Now().Add(-time.Minute)},
	}
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
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Waits on you  2 · oldest 2d") || strings.Index(v, "Teams thread") > strings.Index(v, "Approve?") {
		t.Fatalf("waits view, oldest first:\n%s", v)
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
