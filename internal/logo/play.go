package logo

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// FrameRate is the time between frames; Hold is how long the finished mark
// stays before the player returns. FrameCount frames plus Hold is about 1.5 s.
const (
	FrameRate = 33 * time.Millisecond
	Hold      = 300 * time.Millisecond
)

// Banner draws the mark for a w×h terminal: the animated splash when anim is
// true and it fits, otherwise the largest static drawing that fits, printed
// once. Any key skips to the end. It never fails the caller's command: an
// error is returned for logging only.
func Banner(ctx context.Context, in io.Reader, out io.Writer, w, h int, asc, anim bool) error {
	s, ok := Fit(w, h)
	if !ok {
		return nil
	}
	if !anim || s != Splash {
		_, err := fmt.Fprintln(out, Render(s, asc))
		if err != nil {
			return fmt.Errorf("print banner: %w", err)
		}
		return nil
	}
	p := tea.NewProgram(newPlayer(Frames(Splash, asc, FrameCount)),
		tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out), tea.WithoutSignalHandler())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("play banner: %w", err)
	}
	return nil
}

// player is the bubbletea model that steps through the frames.
type player struct {
	frames []string
	i      int
}

type tickMsg struct{}

func newPlayer(frames []string) player { return player{frames: frames} }

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{} })
}

func (p player) Init() tea.Cmd { return tick(FrameRate) }

func (p player) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case tea.KeyMsg:
		p.i = len(p.frames) - 1
		return p, tea.Quit
	case tickMsg:
		if p.i == len(p.frames)-1 {
			return p, tea.Quit
		}
		p.i++
		if p.i == len(p.frames)-1 {
			return p, tick(Hold)
		}
		return p, tick(FrameRate)
	}
	return p, nil
}

func (p player) View() string {
	if len(p.frames) == 0 {
		return ""
	}
	return strings.TrimRight(p.frames[p.i], "\n") + "\n"
}
