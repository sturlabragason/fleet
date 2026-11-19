package fleet

import (
	"fmt"
	"strings"

	"github.com/sturlabragason/fleet/internal/status"
)

// Pending is what an agent waits on from the user, as the overview's answer
// view shows it (ADR-167): a picker's question and options, a waiting line,
// or with nothing on screen, its open question or blocked report.
type Pending struct {
	Question string   // menu question, waiting line, or the report's text
	Options  []string // the picker's options; none for a waiting line or a report
	Report   string   // "question" or "blocked" when Question is a report
}

// Expect is the text the user saw, question and options, which Reply
// requires unchanged: a pick means nothing against another option list.
func (p Pending) Expect() string {
	parts := p.Options
	if p.Question != "" {
		parts = append([]string{p.Question}, p.Options...)
	}
	return strings.Join(parts, " | ")
}

// Pending reads what the agent waits on right now; an error when nothing.
func (f *Fleet) Pending(name string) (Pending, error) {
	m, ok, err := f.Menu(name)
	if err != nil {
		return Pending{}, err
	}
	if ok {
		return Pending{Question: m.Question, Options: m.Options}, nil
	}
	w, err := f.Prompting(name)
	if err != nil {
		return Pending{}, err
	}
	if w != "" {
		return Pending{Question: w}, nil
	}
	if a := f.Store.Load().Agents[name]; a != nil && a.OpenAsk() {
		return Pending{Question: strings.TrimSpace(a.Report.Summary), Report: a.Report.State}, nil
	}
	return Pending{}, fmt.Errorf("%s waits on nothing", name)
}

// Reply is the user's answer: an option (1-based), y or n, or a short text.
type Reply struct {
	Option int
	YN     string
	Text   string
}

// Gate refuses an answer to a prompt that is not the one the user saw.
func Gate(now Pending, expect string) error {
	if expect == "" {
		return fmt.Errorf("expect is required: the prompt as shown")
	}
	if now.Expect() != expect {
		return fmt.Errorf("prompt changed: now '%s'", status.Cut(now.Expect(), 120))
	}
	return nil
}

// Reply answers what the agent waits on, only when it is still exactly
// expect and the user is not typing in its pane (ADR-61). An option or y/n
// on a picker goes through Choose; text, or y/n with no picker, is sent as
// a steer and recorded as a tweak (ADR-164).
func (f *Fleet) Reply(name string, r Reply, expect string) (string, error) {
	now, err := f.Pending(name)
	if err != nil {
		return "", err
	}
	if err := Gate(now, expect); err != nil {
		return "", err
	}
	if len(now.Options) > 0 {
		return f.replyOption(name, now, r)
	}
	text := strings.TrimSpace(r.Text)
	if text == "" {
		text = map[string]string{"y": "yes", "n": "no"}[r.YN]
	}
	if text == "" {
		return "", fmt.Errorf("nothing to send")
	}
	if why := f.SendBlocker(name, true); why != "" {
		return "", fmt.Errorf("not sent: %s", why)
	}
	if _, err := f.Deliver(name, text, false, false); err != nil {
		return "", err
	}
	return "sent " + status.Cut(text, 40), nil
}

// replyOption picks r's option, or the Yes/No option for y/n, on the picker
// now shows. Choose re-reads the screen and checks the question once more.
func (f *Fleet) replyOption(name string, now Pending, r Reply) (string, error) {
	switch r.YN {
	case "y":
		if r.Option = first(now.Options, "Yes"); r.Option == 0 {
			r.Option = 1
		}
	case "n":
		if r.Option = first(now.Options, "No"); r.Option == 0 {
			return "", fmt.Errorf("no option starting with \"No\"; pick a number")
		}
	}
	if r.Option < 1 || r.Option > len(now.Options) {
		return "", fmt.Errorf("the prompt has options: pick 1-%d", len(now.Options))
	}
	if why := f.SendBlocker(name, false); why != "" {
		return "", fmt.Errorf("not sent: %s", why)
	}
	ex := now.Question
	if ex == "" {
		ex = status.Label(now.Options[r.Option-1])
	}
	c, err := f.Choose(name, r.Option, ex)
	if err != nil {
		return "", err
	}
	return "chose " + c.Label, nil
}
