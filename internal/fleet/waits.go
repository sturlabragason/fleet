package fleet

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/todo"
	"github.com/sturlabragason/fleet/internal/waits"
)

// Waits is everything waiting on the user, oldest first (ADR-167): agents on
// a prompt, an open question or block, or idle with no report, the admirals'
// unread reports, the todo store's open items and agent queues (ADR-164),
// and what extensions add, plus the agents that wait on nothing (running,
// open, quiet) for the w view's tree (ADR-168); waits.Only(…, waits.Waiting)
// is the flat list. A todo store that fails is logged and left out.
func (f *Fleet) Waits(now time.Time) []waits.Item {
	in := waits.Input{Agents: f.Store.Load().Sorted(), Ctx: map[string]string{}}
	names := f.AdmiralNames()
	for _, n := range names {
		in.Reports = append(in.Reports, f.Mail().Peek(mail.BoxOf(n))...)
	}
	_, err := TodoDo(func(ctx context.Context, s todo.Store) error {
		for _, a := range in.Agents {
			if c := s.Context(WorkDir(a)); c != "" {
				in.Ctx[a.Name] = c
			}
		}
		for _, n := range names {
			its, err := s.Admiral(ctx, n)
			if err != nil {
				return fmt.Errorf("admiral %s items: %w", n, err)
			}
			in.Todos = append(in.Todos, its...)
		}
		qs, err := s.Queues(ctx)
		if err != nil {
			return fmt.Errorf("queues: %w", err)
		}
		for _, agent := range slices.Sorted(maps.Keys(qs)) {
			its, err := s.Queue(ctx, agent)
			if err != nil {
				return fmt.Errorf("queue of %s: %w", agent, err)
			}
			in.Todos = append(in.Todos, its...)
		}
		return nil
	})
	if err != nil {
		logx.For("waits").Warn("todo store left out", "err", err)
	}
	for _, e := range exts {
		if e.Waits != nil {
			in.Extra = append(in.Extra, e.Waits(f)...)
		}
	}
	return waits.Build(in, now)
}

// DoneWait completes the todo item behind a w row (d, ADR-168) and logs the
// done note for the admiral that owns it: the row's admiral, or the owner of
// the agent whose queue it is in.
func (f *Fleet) DoneWait(it waits.Item) (todo.Item, error) {
	if !it.Todo() {
		return todo.Item{}, fmt.Errorf("%s is not a todo item", it.Kind)
	}
	adm := it.Admiral
	if it.Kind == waits.Queue {
		adm = ""
		if a, ok := f.Store.Load().Agents[it.Agent]; ok {
			adm = a.Owner()
		}
	}
	return f.DoneItem(adm, it.Agent, it.Ref, "from the overview")
}

// ActWait is Enter on a w row (ADR-177): the item goes to its owner. Work
// for an agent is sent to it as a task, after a wake when it is parked or a
// resume when it has closed, and its todo is completed: the send is now a
// tweak on that agent's queue, which its done report has to close. The
// admiral's and the user's own items are typed into the admiral's pane as
// a prompt and stay open until the admiral closes them. A to that is not ""
// sends the item to that agent instead (> in the w view, the agent in ▶).
// It returns the outcome for the row, e.g. "→ sent to api".
func (f *Fleet) ActWait(it waits.Item, to string) (string, error) {
	if to != "" {
		return f.waitToAgent(it, to)
	}
	switch it.Action() {
	case waits.ActAgent:
		return f.waitToAgent(it, it.Agent)
	case waits.ActAdmiral:
		to := it.Target()
		if err := f.PromptAdmiral(to, it.Task()); err != nil {
			return "", err
		}
		return "→ " + to, nil
	}
	return "", fmt.Errorf("%s rows are not sent anywhere", it.Kind)
}

// waitToAgent sends the item to agent name, waking or resuming it first.
func (f *Fleet) waitToAgent(it waits.Item, name string) (string, error) {
	text := it.Task()
	a, on := f.Store.Load().Agents[name]
	var out string
	switch {
	case !on:
		if _, held, err := f.Resume(name, text); err != nil {
			return "", err
		} else if held {
			out = "→ " + name + " (resume queued)"
		} else {
			out = "→ resumed " + name
		}
		f.Tweak(name, todo.Queued, text)
	case a.Status == string(status.Parked):
		w, err := f.Wake(name, false)
		if err != nil {
			return "", err
		}
		if w.ACP() {
			if _, err := f.Deliver(name, text, false, true); err != nil {
				return "", fmt.Errorf("woke %s, but the send failed (send again once it is up): %w", name, err)
			}
		} else {
			f.markSent(name, text) // fleet typed it, not the user (status.FleetSent)
			f.PastePrompt(w.PaneID, f.Profiles[w.Profile], text, prefs.DefaultReadyTimeout)
			f.Tweak(name, todo.Queued, text)
		}
		out = "→ woke " + name
	case a.Status == string(status.Queued):
		if _, err := f.HoldForQueued(name, text); err != nil {
			return "", err
		}
		f.Tweak(name, todo.Queued, text)
		out = "→ " + name + " (delivered when it starts)"
	default:
		if why, _ := f.SendBlock(name, true); why != "" {
			return "", UserError(why)
		}
		if w, err := f.Prompting(name); err != nil {
			return "", err
		} else if w != "" {
			return "", fmt.Errorf("%s is on a prompt (%s); answer it first", name, w)
		}
		if _, err := f.Deliver(name, text, false, false); err != nil {
			return "", err
		}
		out = "→ sent to " + name
	}
	if it.Todo() {
		if _, err := f.DoneWait(it); err != nil {
			return out, fmt.Errorf("sent to %s, but its todo stays open: %w", name, err)
		}
	}
	return out, nil
}

// PromptAdmiral types one line into the admiral's input box and submits it,
// under the same guards as a report wake (ADR-36): only into an empty box,
// under the wake lock, and Enter only when the box holds exactly the line.
// An ACP admiral gets it as a prompt.
func (f *Fleet) PromptAdmiral(name, line string) error {
	box := mail.BoxOf(name)
	if f.isACP(box) {
		_, err := f.acpCall(box, acphost.Request{Op: acphost.OpPrompt, Text: line})
		return err
	}
	v := f.boxView(box)
	switch {
	case v.Pane == "":
		return fmt.Errorf("admiral %s has no pane", name)
	case !v.Box:
		return fmt.Errorf("can't see admiral %s's input box; try again when it is on screen", name)
	case v.Input != "":
		return fmt.Errorf("admiral %s's input box has text; not typing over it", name)
	}
	release, ok := f.lockWake(box)
	if !ok {
		return fmt.Errorf("fleet is typing into admiral %s's pane; try again", name)
	}
	defer release()
	// Read again under the lock: a report wake may have just typed.
	if v = f.boxView(box); v.Pane == "" || !v.Box || v.Input != "" {
		return fmt.Errorf("admiral %s's input box changed; nothing sent, try again", name)
	}
	if err := f.typeText(v.Pane, line); err != nil {
		return fmt.Errorf("type into admiral %s: %w", name, err)
	}
	if !f.guardEnter(box, v.Pane, line) {
		return fmt.Errorf("admiral %s's input box did not take the line; nothing sent", name)
	}
	return nil
}
