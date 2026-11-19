package ui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/status"
)

// answerView answers one agent's prompt or open question from the overview
// (ADR-167): it shows the exact prompt and options, takes a pick, y/n or a
// short typed reply, confirms, then sends it with the prompt as expect, so a
// prompt that changed meanwhile is refused (fleet.Gate).
type answerView struct {
	agent   string
	p       fleet.Pending
	loaded  bool
	err     string      // the last load or send failure
	typing  bool        // the reply line has the keys
	text    []rune      // the typed reply
	confirm bool        // pick is chosen, ⏎ sends it
	pick    fleet.Reply // what ⏎ sends
	sending bool
}

// replyMax is how long a typed reply may get: a short answer, not a task.
const replyMax = 200

type pendingMsg struct {
	agent string
	p     fleet.Pending
	err   error
}

type replyMsg struct {
	agent, out string
	err        error
}

// openAnswer is r: read what name waits on and show it.
func (m *Model) openAnswer(name string) tea.Cmd {
	if _, ok := m.be.(WaitsBackend); !ok {
		m.msg = "r needs fleet's backend"
		return nil
	}
	if name == "" {
		return nil
	}
	m.ans = &answerView{agent: name}
	return m.loadPending()
}

func (m *Model) loadPending() tea.Cmd {
	wb, name := m.be.(WaitsBackend), m.ans.agent
	return func() tea.Msg {
		p, err := wb.Pending(name)
		return pendingMsg{agent: name, p: p, err: err}
	}
}

func (m *Model) gotPending(msg pendingMsg) {
	a := m.ans
	if a == nil || a.agent != msg.agent {
		return
	}
	a.p, a.loaded, a.confirm, a.typing = msg.p, true, false, false
	if msg.err != nil {
		a.err = msg.err.Error()
	}
}

// gotReply closes the view on success; a refusal stays open on the prompt
// as it is now, with the reason.
func (m *Model) gotReply(msg replyMsg) tea.Cmd {
	a := m.ans
	if a == nil || a.agent != msg.agent {
		return nil
	}
	a.sending = false
	if msg.err == nil {
		m.ans = nil
		m.msg = msg.agent + ": " + msg.out
		return m.loadWaits()
	}
	a.err = msg.err.Error()
	a.loaded = false
	return m.loadPending()
}

func (m *Model) answerKey(k string) tea.Cmd {
	a := m.ans
	switch {
	case k == "ctrl+c":
		return tea.Quit
	case a.sending:
		return nil
	case a.confirm:
		switch k {
		case "enter", "y":
			a.sending = true
			wb, name, r, ex := m.be.(WaitsBackend), a.agent, a.pick, a.p.Expect()
			return func() tea.Msg {
				out, err := wb.Reply(name, r, ex)
				return replyMsg{agent: name, out: out, err: err}
			}
		case "esc", "n":
			a.confirm = false
		}
	case a.typing:
		m.typeKey(k)
	case k == "esc" || k == "q":
		m.ans = nil
	case !a.loaded || a.p.Expect() == "":
		return nil
	case k == "y" || k == "n":
		a.pick, a.confirm, a.err = fleet.Reply{YN: k}, true, ""
	case k == "t" && len(a.p.Options) == 0:
		a.typing, a.err = true, ""
	default:
		if n, err := strconv.Atoi(k); err == nil && n >= 1 && n <= len(a.p.Options) {
			a.pick, a.confirm, a.err = fleet.Reply{Option: n}, true, ""
		}
	}
	return nil
}

// typeKey edits the reply line: ⏎ goes to confirm, Esc back to the prompt.
func (m *Model) typeKey(k string) {
	a := m.ans
	switch k {
	case "esc":
		a.typing = false
	case "enter":
		if strings.TrimSpace(string(a.text)) != "" {
			a.typing, a.confirm, a.pick = false, true, fleet.Reply{Text: string(a.text)}
		}
	case "backspace":
		if len(a.text) > 0 {
			a.text = a.text[:len(a.text)-1]
		}
	case "space":
		k = " "
		fallthrough
	default:
		if utf8.RuneCountInString(k) == 1 && len(a.text) < replyMax {
			a.text = append(a.text, []rune(k)...)
		}
	}
}

// pickText names what the confirm line will send.
func (a *answerView) pickText() string {
	switch {
	case a.pick.Option > 0 && a.pick.Option <= len(a.p.Options):
		return a.p.Options[a.pick.Option-1]
	case a.pick.YN == "y":
		return "yes"
	case a.pick.YN == "n":
		return "no"
	}
	return string(a.text)
}

// answerLines draws the agent, the exact prompt, its options and the one
// line of keys for the current step.
func (m *Model) answerLines() []string {
	a := m.ans
	dim := lipgloss.NewStyle().Foreground(colIdle)
	text := lipgloss.NewStyle().Foreground(colText)
	warn := lipgloss.NewStyle().Foreground(colWaitA)
	what := "prompt on screen"
	if a.p.Report != "" {
		what = a.p.Report + " report"
	}
	lines := []string{" " + lipgloss.NewStyle().Bold(true).Foreground(colText).Render("Answer "+a.agent) + dim.Render(" · "+what), ""}
	if !a.loaded {
		lines = append(lines, dim.Render("  reading the prompt…"))
	} else {
		for l := range strings.SplitSeq(ansi.Wordwrap(a.p.Question, max(m.w-4, 20), ""), "\n") {
			lines = append(lines, "  "+text.Bold(true).Render(l))
		}
		for _, o := range a.p.Options {
			lines = append(lines, "  "+text.Render(o))
		}
	}
	if a.err != "" {
		lines = append(lines, "", "  "+lipgloss.NewStyle().Foreground(colError).Render("✖ "+status.Cut(a.err, max(m.w-6, 20))))
	}
	lines = append(lines, "")
	switch {
	case a.sending:
		lines = append(lines, dim.Render("  sending…"))
	case a.confirm:
		lines = append(lines, "  "+warn.Render(fmt.Sprintf("Send %q to %s?", status.Cut(a.pickText(), 60), a.agent))+dim.Render("  ⏎/y send · esc/n back"))
	case a.typing:
		lines = append(lines, "  reply> "+text.Render(string(a.text))+lipgloss.NewStyle().Foreground(colSelect).Render("▏")+dim.Render("  ⏎ next · esc back"))
	case !a.loaded || a.p.Expect() == "":
		lines = append(lines, dim.Render("  esc close"))
	case len(a.p.Options) > 0:
		lines = append(lines, dim.Render(fmt.Sprintf("  1-%d pick · y yes · n no · esc close", len(a.p.Options))))
	default:
		lines = append(lines, dim.Render("  y yes · n no · t type a reply · esc close"))
	}
	return lines
}
