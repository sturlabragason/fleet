package main

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/tools"
	"github.com/sturlabragason/fleet/internal/wall"
)

// Preview and the right-click menu (DESIGN.md §5e).

// previewTitle is the preview popup's border title; Enter adds the agent to
// the wall when there is no ▶ pane.
func previewTitle(name string, hasTarget bool) string {
	if hasTarget {
		return " " + name + " · preview · ⏎ put in ▶ · q close "
	}
	return " " + name + " · preview · ⏎ add · q close "
}

// previewPopupArgs opens `fleet peek --live NAME` in a popup. exe is already
// shell-quoted.
func previewPopupArgs(exe, name string, hasTarget bool) []string {
	return []string{"display-popup", "-E", "-b", "rounded", "-w", "90%", "-h", "85%",
		"-T", previewTitle(name, hasTarget), exe + " peek --live " + tools.Quote(name)}
}

// menuState is what decides which items the right-click menu shows.
type menuState struct {
	onWall, hasTarget, pinned bool
	targetPinned              bool // the ▶ pane is pinned
	multi                     bool // several admirals: offer "Hand to ▸"
}

// menuArgs is the right-click menu for an agent card at client cell x,y (the
// menu's top-left corner). Items that don't apply are left out, not greyed.
// exe is already shell-quoted.
func menuArgs(exe, name string, x, y int, st menuState) []string {
	// --notify: the result line comes back as a tmux message (focus stays,
	// run-shell output never opens view mode).
	run := func(verb string) string {
		return tmuxCmdString([]string{"run-shell", "-b", exe + " wall " + verb + " --notify " + tools.Quote(name) + " >/dev/null 2>&1"})
	}
	// A pinned ▶ pane is never replaced, so the card goes onto the wall.
	put := "Put in ▶ pane"
	if !st.hasTarget || st.targetPinned {
		put = "Add to wall"
	}
	items := [][3]string{
		{put, "r", run("show --keep-focus")},
		{"Add as a new pane", "a", run("add --keep-focus")},
		{"Preview", "Space", tmuxCmdString(previewPopupArgs(exe, name, st.hasTarget))},
		{"", "", ""},
	}
	if st.pinned {
		items = append(items, [3]string{"Unpin", "p", run("pin")})
	} else {
		items = append(items, [3]string{"Pin", "p", run("pin")})
	}
	if st.onWall {
		items = append(items, [3]string{"Take off the wall", "x", run("hide")})
	}
	if st.multi {
		items = append(items, [3]string{"Hand to ▸", "o", tmuxCmdString([]string{"run-shell", "-b", exe + " admiral give " + tools.Quote(name) + " >/dev/null 2>&1"})})
	}
	// -M: the menu takes the mouse although it is not opened from a tmux
	// mouse event, else clicking an item does nothing (MENU_NOMOUSE).
	// -y is the menu's bottom line in tmux: the top sits at y, below the
	// pointer's row, with the border lines around the items.
	args := []string{"display-menu", "-M", "-T", " " + name + " ", "-x", strconv.Itoa(x), "-y", strconv.Itoa(y + len(items) + 2)}
	for _, it := range items {
		if it[0] == "" {
			args = append(args, "") // separator
			continue
		}
		args = append(args, it[0], it[1], it[2])
	}
	return args
}

// liveModel is `fleet peek --live`: the pane's visible screen with colours,
// redrawn 4x a second and cropped to the popup, keeping the bottom.
type liveModel struct {
	name    string
	noPane  bool
	capture func() (string, error)
	show    func() error // Enter: into the ▶ pane, focus stays put
	screen  string
	w, h    int
	err     error
}

type liveTick struct{}

// liveEvery is the redraw interval.
const liveEvery = 250 * time.Millisecond

func (m *liveModel) Init() tea.Cmd {
	if m.noPane {
		return nil
	}
	return func() tea.Msg { return liveTick{} }
}

func (m *liveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case liveTick:
		m.screen, m.err = m.capture()
		return m, tea.Tick(liveEvery, func(time.Time) tea.Msg { return liveTick{} })
	case tea.KeyMsg:
		if m.noPane {
			return m, tea.Quit
		}
		switch msg.String() {
		case "enter":
			if err := m.show(); err != nil {
				logx.For("peek").Error("live: show", "agent", m.name, "err", err)
			}
			return m, tea.Quit
		case "q", "esc", " ", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *liveModel) View() string {
	if m.noPane {
		return m.name + " has no pane · any key closes"
	}
	if m.err != nil {
		return m.name + ": " + m.err.Error()
	}
	return fitScreen(m.screen, m.w, m.h)
}

// fitScreen crops a captured screen to w x h: trailing blank lines go, then
// the last h lines are kept and each is cut to w cells. ansi.Truncate never
// cuts inside an escape sequence; colours carry across lines as in the pane
// (capture-pane -e), so there is one reset at the very end, not per line.
func fitScreen(s string, w, h int) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r", ""), "\n")
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	if h > 0 && len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	if w > 0 {
		for i, l := range lines {
			lines[i] = ansi.Truncate(l, w, "")
		}
	}
	return strings.Join(lines, "\n") + "\x1b[0m"
}

// cmdPeekLive runs the live view for an agent (the preview popup's command).
func cmdPeekLive(f *fleet.Fleet, name string) error {
	m := &liveModel{name: name}
	pane, err := f.Pane(name)
	if err != nil {
		m.noPane = true
	} else {
		w := wall.New(f.T, fleet.Session)
		w.KeepFocus = true
		m.capture = func() (string, error) { return f.T.Run("capture-pane", "-e", "-p", "-t", pane) }
		if f.Store.Load().Agents[name].ACP() {
			// An acp agent's pane is its host; show the transcript instead.
			m.capture = func() (string, error) { return f.PeekColor(name, 200) }
		}
		m.show = func() error { _, err := w.Show(pane); return err }
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}
