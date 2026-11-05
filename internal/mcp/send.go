package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/sessions"
	"github.com/sturlabragason/fleet/internal/status"
)

// summaryWait bounds fleet_peek summary=true, so an MCP call never hangs on
// the model: comfortably inside a typical client tool timeout.
const summaryWait = 50 * time.Second

// waitingFor is the waiting_for cell: the prompt line, plus the options of
// a numbered menu on screen (" [1 Yes | 2 Yes, and … | 3 No…]"), at most
// 200 runes. Only waiting agents' panes are captured.
func (s *Server) waitingFor(a *board.Agent) string {
	wf := status.Cut(a.LastLine, 70)
	if a.Status != "waiting" || a.PaneID == "" {
		return wf
	}
	text, err := s.F.T.Capture(a.PaneID, 0)
	if err != nil {
		return wf
	}
	m, ok := s.F.Patterns[a.Patterns].Menu(text)
	if !ok {
		return wf
	}
	return status.Cut(wf+MenuCell(m), 200)
}

// MenuCell renders menu options compactly: " [1 Yes | 2 No, and …]". An
// unnumbered ❯ list says so, because those numbers are ours and the user
// cannot see them on screen; answer it by label or option index.
func MenuCell(m status.Menu) string {
	var opts []string
	for _, o := range m.Options {
		n, l, _ := strings.Cut(o, ". ")
		opts = append(opts, n+" "+status.Cut(l, 30))
	}
	out := " [" + strings.Join(opts, " | ") + "]"
	if !m.Numbered {
		out += " (unnumbered: answer by label)"
	}
	return out
}

// send runs fleet_send: text and/or keys to name, names or a team.
func (s *Server) send(a args) (string, error) {
	n := 0
	for _, set := range []bool{a.Name != "", len(a.Names) > 0, a.Team != ""} {
		if set {
			n++
		}
	}
	if n != 1 {
		return "", fmt.Errorf("pass exactly one of name, names or team")
	}
	if a.Text == "" && len(a.Keys) == 0 {
		return "", fmt.Errorf("text or keys is required")
	}
	if err := fleet.CheckKeys(a.Keys); err != nil {
		return "", err
	}
	if a.Name != "" {
		if err := s.sendOne(a, a.Name); err != nil {
			return "", err
		}
		return "sent to " + a.Name, nil
	}
	names := a.Names
	if a.Team != "" {
		ms, err := s.F.Teams.Members(a.Team)
		if err != nil {
			return "", err
		}
		if len(ms) == 0 {
			return "team has no members", nil
		}
		names = ms
	}
	var b strings.Builder
	for _, name := range names {
		if err := s.sendOne(a, name); err != nil {
			fmt.Fprintf(&b, "%s: skipped (%v)\n", name, err)
			continue
		}
		fmt.Fprintf(&b, "%s: sent\n", name)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// sendOne sends to one agent. Text or keys to an agent on a prompt need
// confirmed=true: Enter on a picker approves it (ADR-9).
func (s *Server) sendOne(a args, name string) error {
	if !a.Confirmed {
		w, err := s.F.Prompting(name)
		if err != nil {
			return err
		}
		if w != "" {
			return fleet.WaitingError(w)
		}
	}
	if len(a.Keys) == 0 {
		return s.F.Send(name, a.Text)
	}
	return s.F.SendKeys(name, a.Text, a.Keys)
}

// answer runs fleet_answer: y/n, a numbered option, or an option picked by
// its label (menus whose options are not numbered on screen).
func (s *Server) answer(a args) (string, error) {
	if a.Name == "" {
		return "", fmt.Errorf("name is required")
	}
	given := 0
	for _, b := range []bool{a.Answer != "", a.Option != 0, a.Label != ""} {
		if b {
			given++
		}
	}
	if given != 1 {
		return "", fmt.Errorf(`pass exactly one of answer ("y" or "n"), option (1-9) or label`)
	}
	if a.Answer != "" && a.Answer != "y" && a.Answer != "n" {
		return "", fmt.Errorf(`answer must be "y" or "n"`)
	}
	if a.Option != 0 && (a.Option < 1 || a.Option > 9) {
		return "", fmt.Errorf("option must be 1-9")
	}
	if !a.Confirmed {
		return "", fmt.Errorf("not confirmed: ask the user, then call again with confirmed=true")
	}
	var c fleet.Choice
	var err error
	switch {
	case a.Option != 0:
		c, err = s.F.Choose(a.Name, a.Option, a.Expect)
	case a.Label != "":
		c, err = s.F.ChooseLabel(a.Name, a.Label, a.Expect)
	default:
		c, err = s.F.Answer(a.Name, a.Answer, a.Expect)
	}
	if err != nil {
		return "", err
	}
	what := a.Answer
	if c.Label != "" {
		what = fmt.Sprintf("%q", c.Label)
	}
	out := "answered " + what + " to " + a.Name
	if c.Question != "" {
		out += fmt.Sprintf(" (prompt: %q)", status.Cut(c.Question, 120))
	}
	return out, nil
}

// peek runs fleet_peek: cleaned screen by default, raw on request, or the
// last assistant message from the session log.
func (s *Server) peek(a args) (string, error) {
	if a.Name == "" {
		return "", fmt.Errorf("name is required")
	}
	if a.LastMessage {
		return s.F.LastMessage(a.Name)
	}
	if a.Summary.Flag {
		return s.peekSummary(a.Name)
	}
	n := a.Lines
	if n <= 0 {
		n = 20
	}
	if n > 80 {
		n = 80
	}
	if a.Raw {
		out, err := s.F.Peek(a.Name, 0)
		if err != nil {
			return "", err
		}
		return lastLines(strings.TrimRight(out, "\n "), n), nil
	}
	out, err := s.F.Peek(a.Name, min(4*n, 400))
	if err != nil {
		return "", err
	}
	return lastLines(status.CleanScreen(out), n), nil
}

// peekSummary is fleet_peek summary=true: a few lines from the cheap model
// (DESIGN §6.2, ADR-40). It fails open — when no summary can be made the
// admiral is told to peek at the screen instead.
func (s *Server) peekSummary(name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), summaryWait)
	defer cancel()
	r, err := s.F.PeekSummary(ctx, name)
	if err != nil {
		return "", fmt.Errorf("no summary for %s: %v. Call fleet_peek without summary to read the screen", name, err)
	}
	switch {
	case r.Stale:
		return r.Text + "\n\n(cached " + sessions.Age(time.Since(r.At)) + " ago; the screen has changed since)", nil
	case r.Cached:
		return r.Text + "\n\n(cached; the screen has not changed for " + sessions.Age(time.Since(r.At)) + ")", nil
	}
	return r.Text, nil
}

func lastLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// close runs fleet_close; both the refusal and the result name what the
// close could lose.
func (s *Server) close(a args) (string, error) {
	if a.Name == "" {
		return "", fmt.Errorf("name is required")
	}
	note := s.F.CloseNote(a.Name)
	if note != "" {
		note = ". " + note
	}
	// A leader's squad keeps running unless the close says otherwise, so
	// the warning comes before the confirmation (ADR-53).
	if sq := fleet.SquadNote(a.Name, s.F.Squad(a.Name)); sq != "" && !a.Squad {
		note += ". " + sq
		if !a.Confirmed {
			note += ", or close again with squad=true"
		}
	}
	if !a.Confirmed {
		return "", fmt.Errorf("not confirmed: ask the user, then call again with confirmed=true%s", note)
	}
	closed := ""
	if a.Squad {
		if rs := s.F.SquadCloseAll(a.Name); len(rs) > 0 {
			closed = ". squad: " + fleet.FormatResults("closed", rs)
		}
	}
	if err := s.F.Close(a.Name); err != nil {
		return "", err
	}
	return "closed " + a.Name + note + closed, nil
}

// info runs fleet_info.
func (s *Server) info(a args) (string, error) {
	if a.Name == "" {
		return "", fmt.Errorf("name is required")
	}
	if _, err := s.F.Refresh(); err != nil {
		return "", err
	}
	return s.F.Info(a.Name)
}
